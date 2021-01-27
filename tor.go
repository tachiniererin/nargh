package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/cretz/bine/tor"
)

// tor scraper
type tsc struct {
	t       *tor.Tor
	c       *http.Client
	cookies []*http.Cookie
	headers map[string]string
}

// NewCircuit closes the remaining connections and builds a new identity
func (o *tsc) NewCircuit() error {
	o.c.CloseIdleConnections()
	return o.t.Control.Signal("NEWNYM")
}

func newTorInstance() *tsc {
	// Start tor with some defaults + elevated verbosity
	fmt.Println("Starting and registering onion service, please wait a bit...")
	conf := &tor.StartConf{
		// UseEmbeddedControlConn: true,
		// EnableNetwork: true,
	}

	ctx, _ := context.WithTimeout(context.Background(), 5*time.Minute)
	// defer cancel()

	t, err := tor.Start(ctx, conf)
	if err != nil {
		log.Fatalf("Failed to start tor: %v", err)
	}

	dialer, err := t.Dialer(nil, nil)
	if err != nil {
		log.Fatalf("Could not establish TOR Dialer: %v", err)
	}

	c := &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}}

	return &tsc{t: t, c: c}
}
