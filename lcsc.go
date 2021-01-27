package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// SubCategory of a specific product type
type SubCategory struct {
	ID         int
	Name       string
	URL        string
	ProductNum int `json:"product_num"`
}

// Category holds sub-categories of products
type Category struct {
	SubCategory
	Subs []SubCategory `json:",omitempty"`
}

// SearchResult information
type SearchResult struct {
	Success bool
	Message string
	Code    int
	Result  struct {
		Data             []Product
		CurrentPage      int `json:"current_page"`
		LastPage         int `json:"last_page"`
		TotalPage        int `json:"total_page"`
		Total            int
		RecommendKeyword interface{} `json:"recommendKeyword"`
		Msg              interface{}
	}
}

// Product search result type
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

var reToken = regexp.MustCompile("(?:X-CSRF-TOKEN)[^}]*")

func retry(err error) error {
	if err != nil {
		log.Println(err)
		return ErrRetry
	}
	return nil
}

func newLCSCConn(o *tsc) func() error {
	return func() (err error) {
		if err := o.NewCircuit(); err != nil {
			return err
		}

		var tokenHeader string
		var session, token *http.Cookie
		u, err := url.Parse("https://lcsc.com/products")
		if err != nil {
			return err
		}

		jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		if err != nil {
			return err
		}

		o.c.Jar = jar

		resp, err := o.c.Get(u.String())
		if err != nil {
			return err
		}
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		s := reToken.FindString(string(b))
		s = strings.ReplaceAll(s, "'", "")
		s = strings.ReplaceAll(s, ":", "")
		s = strings.ReplaceAll(s, "X-CSRF-TOKEN", "")
		tokenHeader = strings.TrimSpace(s)

		// extract only the two cookies we need
		for _, cookie := range jar.Cookies(u) {
			if strings.ToLower(cookie.Name) == "lcsc_session" {
				session = &http.Cookie{Name: cookie.Name, Value: cookie.Value}
			} else if strings.ToLower(cookie.Name) == "xsrf-token" {
				token = &http.Cookie{Name: cookie.Name, Value: cookie.Value}
			}
		}

		o.cookies = []*http.Cookie{session, token}
		if o.headers == nil {
			o.headers = make(map[string]string)
		}
		o.headers["X-Csrf-Token"] = tokenHeader

		return nil
	}
}

func getCategories(o *tsc) (c []Category, err error) {
	resp, err := o.c.Get("https://lcsc.com/products")

	if err != nil {
		log.Println(err)
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
				return nil, fmt.Errorf("check products page for changes in the HTML (%w)", err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading standard input: %s", err)
	}

	return
}

func searchProducts(o *tsc, page int, category int) (sr SearchResult, err error) {
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
		req, err := http.NewRequest("POST", "https://lcsc.com/api/products/search", strings.NewReader(args.Encode()))
		for _, cookie := range o.cookies {
			req.AddCookie(cookie)
		}
		for k, v := range o.headers {
			req.Header.Set(k, v)
		}

		resp, err := o.c.Do(req)

		if err != nil {
			log.Println(err)
			return err
		}
		defer resp.Body.Close()
		return json.NewDecoder(resp.Body).Decode(&sr)
	}, func(err error) error {
		if err != nil {
			log.Println(err)
		}
		// handle known possible errors
		if err == io.EOF {
			if verbose {
				log.Println("EOF on request")
			}
			return ErrRetry
		} else if serr, ok := err.(*json.SyntaxError); ok {
			if verbose {
				log.Printf("Invalid json data, retrying (%v)", serr)
			}
			return ErrRetry
		}
		return err
	}, newLCSCConn(o), 5)

	return sr, err
}

func getSubCategory(o *tsc, c SubCategory) {
	var partsu []Product
	var lastPage int

	subCat := func(page int) (err error) {
		sr, err := searchProducts(o, page, c.ID)

		if err != nil {
			return fmt.Errorf("could not fetch sub-category page %d:%d (%w)", c.ID, page, err)
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
			log.Println(err)
			return err
		}

		if verbose {
			log.Printf("Fetching subcategory %s\t(%d) with %d pages\n", c.Name, c.ID, lastPage)
		}

		return nil
	}, retry, newLCSCConn(o), 3)

	if err != nil {
		log.Fatalln(err)
	}

	// go through the pages and collect all the data
	for page := 2; page < lastPage; page++ {
		err := try(func() error { return subCat(page) }, retry, newLCSCConn(o), 3)

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
