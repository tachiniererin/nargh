package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
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

func getCategories() (c []Category, err error) {
	resp, err := http.Get("https://lcsc.com/products")

	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasSuffix(scanner.Text(), "分类数据") { // Fēnlèi shùjù
			if !scanner.Scan() {
				return nil, fmt.Errorf("couldn't find categorical data")
			}
			line := scanner.Text()
			start := strings.Index(line, "'") + 1
			end := strings.LastIndex(line, "'")
			line = line[start:end]

			if err := json.Unmarshal([]byte(line), &c); err != nil {
				return nil, fmt.Errorf("Check products page for changes in the HTML (%v)\n", err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading standard input: %s", err)
	}

	return
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
	err = try(func() error {
		// get search response page
		resp, err := http.PostForm("https://lcsc.com/api/products/search", args)

		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return json.NewDecoder(resp.Body).Decode(&sr)
	}, func(err error) error {
		// handle known possible errors
		if err == io.EOF {
			if verbose {
				log.Println("EOF on request")
			}
			return nil
		} else if serr, ok := err.(*json.SyntaxError); ok {
			if verbose {
				log.Printf("Invalid json data, retrying (%v)", serr)
			}
			return nil
		}
		return err
	}, newCircuit, 5)

	return sr, err
}

func getSubCategory(c SubCategory) {
	var partsu []Product
	var lastPage int

	subCat := func(page int) (err error) {
		sr, err := searchProducts(page, c.ID)

		if err != nil {
			return fmt.Errorf("Could not fetch sub-category page %d:%d (%v)\n", c.ID, page, err)
		}

		if !sr.Success {
			return fmt.Errorf("Search failed")
		}

		// append new parts
		partsu = append(partsu, sr.Result.Data...)
		progress.Increment()
		lastPage = sr.Result.LastPage
		return nil
	}

	// scrape the first page of the category
	// this gives us the number of product pages
	err := try(func() error {
		err := subCat(1)
		if err != nil {
			return err
		}

		if verbose {
			log.Printf("Fetching subcategory %s\t(%d) with %d pages\n", c.Name, c.ID, lastPage)
		}

		return nil
	}, func(error) error { return nil }, newCircuit, 3)

	if err != nil {
		log.Fatalln(err)
	}

	// go through the pages and collect all the data
	for page := 2; page < lastPage; page++ {
		err := try(func() error { return subCat(page) }, func(error) error { return nil }, newCircuit, 3)

		if err != nil {
			log.Fatalln(err)
		}
	}

	if len(partsu) == 0 {
		if verbose {
			log.Printf("Category %d is empty", c.ID)
		}
		return
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

	if meiliClient != nil {
		if err := convertAndImport(meiliClient, partsu); err != nil {
			log.Printf("%+v\n", partsu)
			log.Println(err)
		}
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "    ")
	if err := enc.Encode(partsu); err != nil {
		log.Fatalln(err)
	}
}
