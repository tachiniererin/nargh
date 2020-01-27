package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/cretz/bine/tor"
)

var (
	torInstance *tor.Tor
)

type SubCategory struct {
	ID         int
	Name       string
	URL        string
	ProductNum int `json:"product_num"`
}

type Category struct {
	SubCategory
	Subs []SubCategory `json:",omitempty"`
}

type SearchResult struct {
	Success bool
	Message string
	Code    int
	Result  struct {
		Data             []map[string]interface{}
		CurrentPage      string `json:"current_page"`
		LastPage         int    `json:"last_page"`
		TotalPage        int    `json:"total_page"`
		Total            int
		RecommendKeyword interface{} `json:"recommendKeyword"`
		Msg              interface{}
	}
}

// newCircuit closes the remaining connections and builds a new identity
func newCircuit() error {
	http.DefaultClient.CloseIdleConnections()
	return torInstance.Control.Signal("NEWNYM")
}

func searchProducts(page int, category int) (sr SearchResult, err error) {
	args := url.Values{}
	args.Set("current_page", strconv.Itoa(page))
	args.Set("category", strconv.Itoa(category))
	args.Set("in_stock", "false")
	args.Set("is_RoHS", "false")
	args.Set("show_icon", "false")
	args.Set("search_content", "")

	// retry a few times in case a circuit goes bad
	for i := 0; i < 5; i++ {
		var resp *http.Response
		resp, err = http.PostForm("https://lcsc.com/api/products/search", args)

		if err != nil {
			if err == io.EOF {
				log.Println("Got EOF on request, creating a new circuit.")
				err = newCircuit()
				if err != nil {
					return
				}
				continue
			}
			return
		}
		if err = json.NewDecoder(resp.Body).Decode(&sr); err != nil {
			if err == io.EOF {
				resp.Body.Close()
				log.Println("Got EOF while reading, creating a new circuit.")
				err = newCircuit()
				if err != nil {
					return
				}
				continue
			}
			return
		}
		return
	}

	return sr, errors.New("Giving up after five retries")
}

func dlSub(c SubCategory) {
	var partsu []map[string]interface{}

	// scrape the first page of the category
	// this gives us the number of product pages
	sr, err := searchProducts(1, c.ID)
	if err != nil {
		log.Printf("Category %d:%d\n", c.ID, 1)
		log.Printf("Error: %v\n", err)
		return
	}

	// get a new identity if we get told off
	if !sr.Success {
		log.Printf("Server told us to back off, creating a new circuit")
		if err := newCircuit(); err != nil {
			log.Fatalf("Failed to create a new circuit: %v", err)
		}
	}

	log.Printf("Fetching subcategory %s\t(%d) with %d pages\n", c.Name, c.ID, sr.Result.TotalPage)

	// append new parts
	partsu = append(partsu, sr.Result.Data...)

	// go through the pages and collect all the data
	for page := 2; page < sr.Result.LastPage; page++ {
		sr, err := searchProducts(page, c.ID)
		if err != nil {
			log.Printf("Category %d:%d\n", c.ID, page)
			log.Printf("Error: %v\n", err)
			return
		}

		if !sr.Success {
			log.Printf("Server told us to back off, creating a new circuit")
			page--
			if err := newCircuit(); err != nil {
				log.Fatalf("Failed to create a new circuit: %v", err)
			}
			continue
		}

		// append new parts
		partsu = append(partsu, sr.Result.Data...)
	}

	// write the data nicely indented to disk
	f, err := os.Create(fmt.Sprintf("json/%d.json", c.ID))
	if err != nil {
		log.Fatalln(err)
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "    ")
	if err := enc.Encode(partsu); err != nil {
		log.Fatalln(err)
	}
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

	if len(os.Args) == 2 {
		// get a single category
		id, err := strconv.Atoi(os.Args[1])
		if err != nil {
			log.Fatalf("Could not parse category to get: %s (%v)", os.Args[1], err)
		}
		categories = []Category{Category{Subs: []SubCategory{SubCategory{ID: id, Name: os.Args[1]}}}}
	} else {
		// load categories, these are from the JS in https://lcsc.com/products
		// manually update if needed
		b, err := ioutil.ReadFile("categories.json")
		if err != nil {
			log.Fatalf("Could not load categories.json (%v)\n", err)
		}

		if err := json.Unmarshal(b, &categories); err != nil {
			log.Fatalf("Check categories.json for errors (%v)\n", err)
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
	dialCtx, dialCancel := context.WithTimeout(context.Background(), time.Minute)
	defer dialCancel()

	// set up the connection to the proxy
	dialer, err := torInstance.Dialer(dialCtx, nil)
	if err != nil {
		log.Fatalf("Could not establish TOR Dialer: %v", err)
	}

	// set up the default http client with our tor proxy
	http.DefaultClient = &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}}

	for _, c := range categories {
		log.Printf("Fetching category %s (%d)\n", c.Name, c.ID)
		for _, sc := range c.Subs {
			dlSub(sc)
		}
	}

	log.Println("Scraping finished.")
}
