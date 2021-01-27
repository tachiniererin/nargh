package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/cheggaaa/pb/v3"
	"github.com/meilisearch/meilisearch-go"
	"github.com/zeebo/blake3"
	"golang.org/x/sync/semaphore"
	"golang.org/x/text/unicode/norm"
)

// PDF MeiliSearch type
type PDF struct {
	ID       string
	Filename string
	Content  string

	Updated time.Time
}

func getEnqueued(client *meilisearch.Client) int {
	var enqueued int
	list, err := client.Updates("pdf").List()
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
		Host:   "http://127.0.0.1:7700",
		APIKey: "",
	}, http.Client{
		Timeout: 10 * time.Second,
	})

	// Create an index if your index does not already exist
	_, err := client.Indexes().Create(meilisearch.CreateIndexRequest{
		UID:        "pdf",
		Name:       "PDF",
		PrimaryKey: "ID",
	})
	if err != nil {
		fmt.Println(err)
	}
	return client
}

func convertAndImport(client *meilisearch.Client, filePath string) error {
	cmd := exec.Command("pdftotext", "-layout", filePath, "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	b, err := ioutil.ReadAll(stdout)
	if err != nil {
		log.Fatalf("error reading stdout: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		log.Printf("could not convert %s: %v", filePath, err)
		return nil
	}

	b = norm.NFC.Bytes(b)

	h := blake3.New()
	if _, err := h.Write(b); err != nil {
		log.Fatal(err)
	}

	doc := PDF{
		ID:       hex.EncodeToString(h.Sum([]byte{})),
		Filename: path.Base(filePath),
		Content:  string(b),
		Updated:  time.Now(),
	}

	_, err = client.Documents("pdf").AddOrUpdate([]PDF{doc})
	return err
}

func importFolder(client *meilisearch.Client, path string) {
	var maxWorkers = runtime.NumCPU()
	var sem = semaphore.NewWeighted(int64(maxWorkers))
	var ctx = context.TODO()

	files, err := ioutil.ReadDir(path)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Pushing data to MeiliSearch...")

	bar := pb.Full.Start(len(files))

	for _, file := range files {
		bar.Increment()

		if file.IsDir() || file.Name() == "" {
			continue
		}

		if !strings.HasSuffix(file.Name(), "pdf") {
			continue
		}

		if err := sem.Acquire(ctx, 1); err != nil {
			log.Printf("Failed to acquire semaphore to start new worker: %v", err)
			break
		}

		go func(file string) {
			defer sem.Release(1)
			fn := path + file
			if err := convertAndImport(client, fn); err != nil {
				log.Fatalf("could not convert %s: %v", fn, err)
			}
		}(file.Name())
	}

	if err := sem.Acquire(ctx, int64(maxWorkers)); err != nil {
		log.Fatalf("Failed to wait for workers to finish: %v", err)
	}

	bar.Finish()

	fmt.Println("Waiting for updates to finish...")

	var updateCount = getEnqueued(client)

	bar = pb.Full.Start(updateCount)

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

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	client := newMeiliClient()
	importFolder(client, "pdf/")

	fmt.Println("Waiting for updates to finish...")

	var updateCount = getEnqueued(client)

	bar := pb.Full.Start(updateCount)

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
