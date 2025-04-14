package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	apiURL        = os.Getenv("OMEKA_API_URL")
	keyIdentity   = os.Getenv("OMEKA_KEY_IDENTITY")
	keyCredential = os.Getenv("OMEKA_KEY_CREDENTIAL")
	port          = getEnvDefault("PORT", "9145")
	debug         = strings.ToLower(getEnvDefault("DEBUG", "false")) == "true"

	scrapeIntervalSeconds, _ = strconv.Atoi(getEnvDefault("SCRAPE_INTERVAL_SECONDS", "30"))

	httpClient = &http.Client{Timeout: 10 * time.Second}

	itemCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "omeka_items_total",
		Help: "Total number of Omeka S items",
	})
	itemSetCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "omeka_item_sets_total",
		Help: "Total number of Omeka S item sets",
	})
	mediaCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "omeka_media_total",
		Help: "Total number of Omeka S media",
	})
	userCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "omeka_users_total",
		Help: "Total number of Omeka S users",
	})
	itemCountPerSet = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "omeka_items_per_set",
			Help: "Number of items per item set",
		},
		[]string{"item_set_id", "item_set_title"},
	)
)

type itemSet struct {
	ID    int    `json:"o:id"`
	Title string `json:"o:title"`
}

func getEnvDefault(key, fallback string) string {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	return val
}

func init() {
	prometheus.MustRegister(itemCount)
	prometheus.MustRegister(itemSetCount)
	prometheus.MustRegister(mediaCount)
	prometheus.MustRegister(userCount)
	prometheus.MustRegister(itemCountPerSet)

	if apiURL == "" || keyIdentity == "" || keyCredential == "" {
		log.Fatal("OMEKA_API_URL, OMEKA_KEY_IDENTITY, and OMEKA_KEY_CREDENTIAL must be set")
	}
}

// Append authentication credentials to a URL
func withAuth(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		log.Printf("Invalid URL: %s", rawURL)
		return rawURL
	}
	q := u.Query()
	q.Set("key_identity", keyIdentity)
	q.Set("key_credential", keyCredential)
	u.RawQuery = q.Encode()
	return u.String()
}

// Get total count from Omeka endpoint via omeka-s-total-results header
func fetchTotal(endpoint string) int {
	fullURL := fmt.Sprintf("%s/%s", apiURL, endpoint)
	if strings.Contains(endpoint, "?") {
		fullURL += "&per_page=1"
	} else {
		fullURL += "?per_page=1"
	}
	fullURL = withAuth(fullURL)

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		log.Printf("Error building GET request for %s: %v", endpoint, err)
		return 0
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Error performing GET request to %s: %v", endpoint, err)
		return 0
	}
	defer resp.Body.Close()

	totalHeader := resp.Header.Get("omeka-s-total-results")
	if totalHeader == "" {
		log.Printf("Missing omeka-s-total-results header for %s", endpoint)
		return 0
	}

	totalInt, err := strconv.Atoi(totalHeader)
	if err != nil {
		log.Printf("Failed to parse omeka-s-total-results header for %s: %v", endpoint, err)
		return 0
	}

	if debug {
		log.Printf("[DEBUG] %s → omeka-s-total-results: %d", endpoint, totalInt)
	}
	return totalInt
}

// Fetch list of item sets
func fetchItemSets() []itemSet {
	url := withAuth(fmt.Sprintf("%s/item_sets?per_page=1000", apiURL))
	resp, err := httpClient.Get(url)
	if err != nil {
		log.Printf("Error fetching item sets: %v", err)
		return nil
	}
	defer resp.Body.Close()

	var sets []itemSet
	if err := json.NewDecoder(resp.Body).Decode(&sets); err != nil {
		log.Printf("Error decoding item sets: %v", err)
		return nil
	}

	if debug {
		log.Printf("[DEBUG] fetched %d item sets", len(sets))
	}
	return sets
}

// Count items in a specific item set
func fetchItemsInSet(setID int) int {
	return fetchTotal(fmt.Sprintf("items?item_set_id=%d", setID))
}

// Collect and expose all metrics
func updateMetrics() {
	itemCount.Set(float64(fetchTotal("items")))
	itemSetCount.Set(float64(fetchTotal("item_sets")))
	mediaCount.Set(float64(fetchTotal("media")))
	userCount.Set(float64(fetchTotal("users")))

	itemCountPerSet.Reset()

	sets := fetchItemSets()
	for _, s := range sets {
		count := fetchItemsInSet(s.ID)
		itemCountPerSet.WithLabelValues(strconv.Itoa(s.ID), s.Title).Set(float64(count))
		if debug {
			log.Printf("[DEBUG] Set ID %d (%s) has %d items", s.ID, s.Title, count)
		}
	}
}

func main() {
	go func() {
		for {
			updateMetrics()
			time.Sleep(time.Duration(scrapeIntervalSeconds) * time.Second)
		}
	}()

	http.Handle("/metrics", promhttp.Handler())

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("Omeka exporter running on :%s/metrics (interval %ds, debug: %v)", port, scrapeIntervalSeconds, debug)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
