package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

const (
	defaultAddr = "localhost:3000"
)

type Fetcher func() ([]key.NodePublic, error)
type Searcher func(n key.NodePublic) bool

func main() {
	addr := flag.String("addr", defaultAddr, "")
	nodesFile := flag.String("path", "", "/path/to/nodes.json")
	flag.Parse()

	var interval time.Duration
	var fetcher Fetcher
	var searcher Searcher

	interval = time.Minute
	fetcher = func() ([]key.NodePublic, error) {
		var nodes []key.NodePublic
		err := readJSONFile(*nodesFile, &nodes)
		return nodes, err
	}

	var lock sync.RWMutex
	var nodes []key.NodePublic
	var lastUpdate uint32

	searcher = func(n key.NodePublic) bool {
		now := uint32(time.Now().Unix())

		if now > atomic.LoadUint32(&lastUpdate)+uint32(interval.Seconds()) {
			atomic.StoreUint32(&lastUpdate, now)
			log.Println("fetcher", "fetching")
			_nodes, err := fetcher()
			if err != nil {
				log.Println("fetcher", err)
			} else {
				lock.Lock()
				nodes = _nodes
				lock.Unlock()
				log.Println("fetcher", "updated")
			}
		}

		lock.RLock()
		defer lock.RUnlock()
		for i := range nodes {
			if n.Compare(nodes[i]) == 0 {
				return true
			}
		}
		return false
	}

	http.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var buf = bytes.NewBuffer(nil)
		var req tailcfg.DERPAdmitClientRequest
		err := json.NewDecoder(io.TeeReader(io.LimitReader(r.Body, 1<<13), buf)).Decode(&req)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var resp tailcfg.DERPAdmitClientResponse

		resp.Allow = searcher(req.NodePublic)

		if resp.Allow {
			log.Println("allowed", req.NodePublic, req.Source)
		}

		b, err := json.Marshal(resp)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(b)
	}))

	log.Println("serving", *addr)
	err := http.ListenAndServe(*addr, nil)
	if err != nil {
		os.Exit(1)
	}
}

func readJSONFile(path string, v interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}
