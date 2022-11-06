package main

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

const (
	httpServerUrl = "http://localhost:8080"
	httpAddr      = ":8080"
	promhttpAddr  = ":9000"
)

var (
	http200RequestCounter = 0
	http500RequestCounter = 0
	twoHundredmutex       = &sync.Mutex{}
	fiveHundredmutex      = &sync.Mutex{}
	up                    = prometheus.NewDesc(
		prometheus.BuildFQName("httpserver", "", "up"),
		"Last query successful.",
		nil, nil,
	)
)

//Http Message json structure
type HttpRespStructure struct {
	Http200Requestcounter float64 `json:"http200Requestcounter"`
	Http500Requestcounter float64 `json:"http500Requestcounter"`
}
type exportedMetrics []struct {
	desc    *prometheus.Desc
	eval    func(stats *HttpRespStructure) float64
	valType prometheus.ValueType
}
type MetricCollector struct {
	client     *http.Client
	httpServer *url.URL
	Stats      *HttpRespStructure
	metrics    exportedMetrics
}

func NewCollector(client *http.Client, url *url.URL) *MetricCollector {
	return &MetricCollector{
		Stats:      &HttpRespStructure{},
		client:     client,
		httpServer: url,
		metrics: exportedMetrics{
			{
				desc: prometheus.NewDesc(
					prometheus.BuildFQName("http", "request", "200counter"),
					"http.requests.counter",
					nil, prometheus.Labels{"counter": "twohundred"},
				),
				eval:    func(stats *HttpRespStructure) float64 { return stats.Http200Requestcounter },
				valType: prometheus.CounterValue,
			},
			{
				desc: prometheus.NewDesc(
					prometheus.BuildFQName("http", "request", "500counter"),
					"http.requests.counter",
					nil, prometheus.Labels{"counter": "fivehundred"},
				),
				eval:    func(stats *HttpRespStructure) float64 { return stats.Http500Requestcounter },
				valType: prometheus.CounterValue,
			},
		},
	}
}

// Describe
func (e *MetricCollector) Describe(ch chan<- *prometheus.Desc) {
	// register desc for up down metric
	ch <- up
	// register other descs
	for _, metric := range e.metrics {
		ch <- metric.desc
	}
}

// Collect
func (e *MetricCollector) Collect(ch chan<- prometheus.Metric) {
	err := e.fetchStatsEndpoint()
	if err != nil {
		ch <- prometheus.MustNewConstMetric(up, prometheus.GaugeValue, float64(0)) // set target down
		log.Errorf("Failed getting /stats endpoint of target: " + err.Error())
		return
	}
	ch <- prometheus.MustNewConstMetric(up, prometheus.GaugeValue, float64(1))
	for _, i := range e.metrics {
		ch <- prometheus.MustNewConstMetric(i.desc, i.valType, i.eval(e.Stats))
	}
}

// fetchStatsEndpoint
func (e *MetricCollector) fetchStatsEndpoint() error {

	response, err := e.client.Get(e.httpServer.String() + "/stats")
	if err != nil {
		log.Errorf("Could not fetch stats endpoint of target: %v", e.httpServer.String())
		return err
	}

	defer response.Body.Close()

	bodyBytes, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Error("Can't read body of response")
		return err
	}
	log.Info(string(bodyBytes))
	err = json.Unmarshal(bodyBytes, &e.Stats)
	if err != nil {
		log.Error("Could not parse JSON response for target")
		return err
	}

	return nil
}

// twoHundred
func twoHundred(w http.ResponseWriter, r *http.Request) {
	twoHundredmutex.Lock()
	http200RequestCounter++
	twoHundredmutex.Unlock()
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "HTTP Endpoint OK!"}`))
}

// fiveHundred
func fiveHundred(w http.ResponseWriter, r *http.Request) {
	fiveHundredmutex.Lock()
	http500RequestCounter++
	fiveHundredmutex.Unlock()
	// simulate 500 eror code
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(`{"message": "HTTP Endpoint Internal Error"}`))
}

// stats
func stats(w http.ResponseWriter, r *http.Request) {
	log.Infof("HttpServer statistics")
	// get stats
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(`{"http200Requestcounter":` + strconv.Itoa(http200RequestCounter) + `,"http500Requestcounter":` + strconv.Itoa(http500RequestCounter) + `}`))
}

// router
func router() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/test200", twoHundred)
	m.HandleFunc("/test500", fiveHundred)
	m.HandleFunc("/stats", stats)
	return m
}
func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan bool, 1)

	server := &http.Server{
		Addr:    httpAddr,
		Handler: router(),
	}
	log.Infof("HttpServer listening on '%s'", httpAddr)
	go func() {
		log.Fatal(server.ListenAndServe())
	}()

	httpServerURL, err := url.Parse(httpServerUrl)

	if err != nil {
		log.Fatalf("failed to parse beat.uri, error: %v", err)
	}
	// register prometheus exporter
	httpClient := &http.Client{}
	exporter := NewCollector(httpClient, httpServerURL)
	prometheus.MustRegister(exporter)

	http.Handle("/metrics", promhttp.Handler())
	log.Infof("PromHttpServer listening on '%s'", promhttpAddr)
	go func() {
		log.Fatal(http.ListenAndServe(promhttpAddr, nil))
	}()

	go func() {
		sig := <-sigs
		log.Info(sig)
		done <- true
	}()
	<-done
	log.Info("Exiting")
}
