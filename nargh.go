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
	"sort"
	"strconv"
	"time"

	"github.com/cretz/bine/tor"
)

var (
	torInstance *tor.Tor
	datasheets  = make(map[string]bool)
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
		Data             []Product
		CurrentPage      string `json:"current_page"`
		LastPage         int    `json:"last_page"`
		TotalPage        int    `json:"total_page"`
		Total            int
		RecommendKeyword interface{} `json:"recommendKeyword"`
		Msg              interface{}
	}
}

type Product struct {
	ID     int    `json:"id"`
	Number string `json:"number"`
	Info   struct {
		Number        string  `json:"number"`
		Unit          string  `json:"unit"`
		Min           int     `json:"min"`
		Max           int     `json:"max"`
		PreUnit       int     `json:"pre_unit"`
		Weight        float32 `json:"weight"`
		PackageMethod string  `json:"packagemethod"`
		Packaging     string  `json:"packaging"`
		Step          int     `json:"step"`
		Title         string  `json:"title"`
	} `json:"info"`
	URL          string `json:"url"`
	Manufacturer struct {
		EN   string `json:"en"`
		Logo string `json:"logo"`
	} `json:"manufacturer"`
	Stock        int                 `json:"stock"`
	Images       []map[string]string `json:"images"`
	Datasheet    map[string]string   `json:"datasheet"`
	Tags         []string            `json:"tags"`
	Package      string              `json:"package"`
	Categories   []string            `json:"categories"`
	Status       string              `json:"status"`
	AutoDown     interface{}         `json:"auto_down"` // can be string or bool
	StockSZ      int                 `json:"stock_sz"`
	StockJS      int                 `json:"stock_js"`
	StockHK      int                 `json:"stock_hk"`
	HotSort      int                 `json:"hot_sort"`
	Attributes   interface{}         // map or array
	DiscountType int                 `json:"discount_type"`
	Description  string              `json:""`
	Price        [][]interface{}     `json:"price"`
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
	var partsu []Product

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

	for _, part := range partsu {
		if len(part.Datasheet) > 0 {
			datasheets[part.Datasheet["pdf"]] = true
		}
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
