package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/cheggaaa/pb/v3"
	"github.com/cretz/bine/tor"
	"github.com/meilisearch/meilisearch-go"
)

var (
	torInstance    *tor.Tor
	datasheets     = make(map[string]bool)
	meiliClient    *meilisearch.Client
	categoryID     int
	importPath     string
	meiliHost      string
	verbose        bool
	totalPageCount int64
	parallel       int
	progress       *pb.ProgressBar
	ErrRetry       = errors.New("retry last action")
)

func init() {
	flag.StringVar(&meiliHost, "meilihost", "", "meilisearch host address")
	flag.StringVar(&importPath, "path", "", "path to import json files from")
	flag.BoolVar(&verbose, "verbose", false, "print more log messages")
	flag.IntVar(&categoryID, "category", 0, "category id to fetch")
	flag.IntVar(&parallel, "parallel", 1, "create n amount of tor nodes to send requests from")
}

// try, repeat, yeet
// check returns nil if the error means it should be retried or the error otherwise
// repeat is called when an error occurs and before λ is being called again
func try(λ func() error, check func(error) error, repeat func() error, tries int) error {
	for i := 0; i < tries; i++ {
		err := λ()
		if err != nil {
			if err := check(err); err != nil && !errors.Is(err, ErrRetry) {
				return err
			}
			if err := check(repeat()); err != nil {
				return err
			}
		} else {
			return nil
		}
	}
	return fmt.Errorf("Giving up after %d tries", tries)
}

func main() {
	var categories []Category
	var err error

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	log.Println(`
    _____ _____ _____ _____ _____ 
   |   | |  _  | __  |   __|  |  |
   | | | |     |    -|  |  |     |
   |_|___|__|__|__|__|_____|__|__|
   Your friendly LCSC scraper.
   `)

	flag.Parse()

	var scrapers = make([]*tsc, parallel)
	var done = make([]chan bool, parallel)

	if meiliHost != "" {
		meiliClient = newMeiliClient()
	}

	if importPath != "" {
		importFolder(importPath)
		return
	}

	// start TOR so that we can scrape it faster :3
	log.Println("Starting TOR...")
	for i := 0; i < parallel; i++ {
		log.Printf("Starting instance %d", i+1)
		scrapers[i] = newTorInstance()
		newLCSCConn(scrapers[i])()
		done[i] = make(chan bool)
	}

	if categoryID != 0 {
		// get a single category
		categories = []Category{{Subs: []SubCategory{{ID: categoryID, Name: ""}}}}
	} else {
		log.Println("Fetching categories from LCSC...")
		categories, err = getCategories(scrapers[0])
		if err != nil {
			log.Fatalln(err)
		}
	}

	progress = pb.Full.Start64(9903) // chosen by fair dice roll, err running it once i mean

	workers := make(chan SubCategory, parallel)

	for i := 0; i < parallel; i++ {
		go func(o *tsc, pool chan SubCategory, done chan bool) {
			select {
			case <-done:
				return
			case sc := <-pool:
				getSubCategory(o, sc)
			}
		}(scrapers[i], workers, done[i])
	}

	for _, c := range categories {
		if verbose {
			log.Printf("Fetching category %s (%d)\n", c.Name, c.ID)
		}
		for _, sc := range c.Subs {
			workers <- sc
			progress.Increment()
		}
	}

	for i := 0; i < parallel; i++ {
		// wait for process to finish and close down the connection
		done[i] <- true
		scrapers[i].t.Close()
	}

	progress.Finish()

	log.Printf("Total page count was: %d", totalPageCount)

	// sort the datasheets and write the urls to file
	f, err := os.Create("pdf/datasheets.txt")
	if err != nil {
		log.Fatalf("Could not write list of datasheets: %v", err)
	}
	urls := make([]string, 0, len(datasheets))
	for k := range datasheets {
		urls = append(urls, k)
	}
	sort.Strings(urls)
	for _, url := range urls {
		_, err := fmt.Fprintln(f, url)
		if err != nil {
			log.Fatalf("Could not write line: %v", err)
		}
	}

	log.Println("Scraping finished.")
}
