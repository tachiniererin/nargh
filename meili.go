package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cheggaaa/pb/v3"
	"github.com/meilisearch/meilisearch-go"
)

// Document type for MeiliSearch
type Document struct {
	ID        string
	MPN       string
	Weight    float32
	Packaging string
	Title     string

	URL          string
	Manufacturer string
	Stock        int
	Datasheet    []string

	Package    string
	Categories []string
	Status     string
	StockSZ    int
	StockJS    int
	StockHK    int
	Updated    time.Time
}

func getEnqueued(client *meilisearch.Client) int {
	var enqueued int
	list, err := client.Updates("lcsc").List()
	if err != nil {
		log.Fatalln(err)
	}

	for _, u := range list {
		if u.Status == meilisearch.UpdateStatusEnqueued {
			enqueued++
		}
	}

	return enqueued
}

func newMeiliClient() *meilisearch.Client {
	var client = meilisearch.NewClientWithCustomHTTPClient(meilisearch.Config{
		Host:   meiliHost,
		APIKey: "",
	}, http.Client{
		Timeout: 10 * time.Second,
	})

	// Create an index if your index does not already exist
	_, err := client.Indexes().Create(meilisearch.CreateIndexRequest{
		UID:        "lcsc",
		Name:       "LCSC",
		PrimaryKey: "ID",
	})
	if err != nil {
		fmt.Println(err)
		// os.Exit(1)
	}
	return client
}

func convertAndImport(client *meilisearch.Client, products []Product) error {
	var documents []Document
	updated := time.Now()

	for _, p := range products {
		datasheets := make([]string, 0, len(p.Datasheet))

		for _, d := range p.Datasheet {
			datasheets = append(datasheets, d)
		}

		d := Document{
			ID:        p.Number,
			MPN:       p.Info.Number,
			Weight:    p.Info.Weight,
			Packaging: p.Info.Packaging,
			Title:     p.Info.Title,

			URL:          p.URL,
			Manufacturer: p.Manufacturer.EN,
			Stock:        p.Stock,
			Datasheet:    datasheets,

			Package:    p.Package,
			Categories: p.Categories,
			Status:     p.Status,
			StockSZ:    p.StockSZ,
			StockJS:    p.StockJS,
			StockHK:    p.StockHK,
			Updated:    updated,
		}

		documents = append(documents, d)
	}

	_, err := client.Documents("lcsc").AddOrUpdate(documents)
	return err
}

func importFolder(path string) {

	client := newMeiliClient()

	files, err := ioutil.ReadDir(path)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Pushing data to MeiliSearch...")

	bar := pb.StartNew(len(files))

	for _, file := range files {
		var products []Product

		bar.Increment()

		if !strings.HasSuffix(file.Name(), "json") {
			continue
		}

		f, err := os.Open("../json/" + file.Name())
		if err != nil {
			log.Fatalln(err)
		}

		dec := json.NewDecoder(f)

		if err := dec.Decode(&products); err != nil {
			log.Fatalln(err)
		}

		if products == nil {
			continue
		}

		if err := convertAndImport(client, products); err != nil {
			fmt.Println(file.Name())
			fmt.Println(err)
			os.Exit(1)
		}
	}

	bar.Finish()

	fmt.Println("Waiting for updates to finish...")

	var updateCount = getEnqueued(client)

	bar = pb.StartNew(updateCount)

	for {
		running := getEnqueued(client)

		if running == 0 {
			break
		}

		bar.SetCurrent(int64(updateCount - running))
		time.Sleep(time.Second)
	}
	bar.Finish()
}
