package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"time"

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
	progress       *pb.ProgressBar
)

func init() {
	flag.StringVar(&meiliHost, "meilihost", "", "meilisearch host address")
	flag.StringVar(&importPath, "path", "", "path to import json files from")
	flag.BoolVar(&verbose, "verbose", false, "print more log messages")
	flag.IntVar(&categoryID, "category", 0, "category id to fetch")
}

// newCircuit closes the remaining connections and builds a new identity
func newCircuit() error {
	http.DefaultClient.CloseIdleConnections()
	return torInstance.Control.Signal("NEWNYM")
}

// try, repeat, yeet
// check returns nil if the error means it should be retried or the error otherwise
// repeat is called when an error occurs and before λ is being called again
func try(λ func() error, check func(error) error, repeat func() error, tries int) error {
	for i := 0; i < tries; i++ {
		err := λ()
		if err != nil {
			if err := check(err); err != nil {
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

	log.Println(`
    _____ _____ _____ _____ _____ 
   |   | |  _  | __  |   __|  |  |
   | | | |     |    -|  |  |     |
   |_|___|__|__|__|__|_____|__|__|
   Your friendly LCSC scraper.
   `)

	flag.Parse()

	if meiliHost != "" {
		meiliClient = newMeiliClient()
	}

	if importPath != "" {
		importFolder(importPath)
		return
	}

	if categoryID != 0 {
		// get a single category
		categories = []Category{Category{Subs: []SubCategory{SubCategory{ID: categoryID, Name: ""}}}}
	} else {
		log.Println("Fetching categories from LCSC...")
		categories, err = getCategories()
		if err != nil {
			log.Fatalln(err)
		}
	}

	// start TOR so that we can scrape it faster :3
	log.Println("Starting TOR...")
	torInstance, err = tor.Start(context.TODO(), nil)
	if err != nil {
		log.Fatalln(err)
	}
	defer torInstance.Close()

	// wait at most a minute to start tor
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer dialCancel()

	// set up the connection to the proxy
	dialer, err := torInstance.Dialer(dialCtx, nil)
	if err != nil {
		log.Fatalf("Could not establish TOR Dialer: %v", err)
	}

	// set up the default http client with our tor proxy
	http.DefaultClient = &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}}

	progress = pb.Full.Start64(9903) // chosen by fair dice roll, err running it once i mean

	for _, c := range categories {
		if verbose {
			log.Printf("Fetching category %s (%d)\n", c.Name, c.ID)
		}
		for _, sc := range c.Subs {
			getSubCategory(sc)
			progress.Increment()
		}
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
