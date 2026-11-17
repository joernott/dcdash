// SPDX-License-Identifier: GPL-3.0-or-later

// Package metrics provides the optional Prometheus endpoint /metrics: HTTP
// requests per route, audit events, the server certificate's expiry, the state
// of the backends (the checks of the status page) and Go runtime metrics.
//
// Metrics use a private registry, so nothing registered by libraries in the
// global default registry is exposed.
package metrics

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/status"
	"github.com/joernott/dcdash/internal/version"
)

const namespace = "dcdash"

// Metrics holds the registry and the metrics dcdash updates itself.
type Metrics struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	audit    *prometheus.CounterVec
}

// New creates the registry with the build information, HTTP, audit, Go
// runtime and process metrics.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "http_requests_total",
			Help: "HTTP requests by route pattern, method and status code.",
		}, []string{"route", "method", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "http_request_duration_seconds",
			Help:    "Duration of HTTP requests by route pattern (console websockets excluded).",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		}, []string{"route"}),
		audit: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "audit_events_total",
			Help: "Audit log entries by action and result, e.g. failed logins (auth.login, failure).",
		}, []string{"action", "result"}),
	}
	v := version.Get()
	build := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace, Name: "build_info",
		Help: "Build information; the value is always 1.",
	}, []string{"version", "commit", "goversion"})
	build.WithLabelValues(v.Version, v.Commit, v.GoVersion).Set(1)
	m.reg.MustRegister(m.requests, m.duration, m.audit, build,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// AuditEvent counts an audit log entry (see audit.Log.OnRecord).
func (m *Metrics) AuditEvent(action, result string) {
	m.audit.WithLabelValues(action, result).Inc()
}

// AddCertificate exports the expiry of the server certificate. notAfter
// returns false while there is no certificate.
func (m *Metrics) AddCertificate(notAfter func() (time.Time, bool)) {
	desc := prometheus.NewDesc(namespace+"_certificate_expiry_timestamp_seconds",
		"Expiry of the server certificate (Unix time); missing while there is none.", nil, nil)
	m.reg.MustRegister(collector{descs: []*prometheus.Desc{desc}, collect: func(ch chan<- prometheus.Metric) {
		if t, ok := notAfter(); ok {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(t.Unix()))
		}
	}})
}

// backendTimeout limits the status checks run during a scrape; it is a bit
// more than status.Timeout, the limit of each check.
const backendTimeout = status.Timeout + time.Second

// AddBackends exports the result of the status checks (database, directory,
// certificate, Proxmox, DNS, DHCP). They run on every scrape.
func (m *Metrics) AddBackends(run func(context.Context) status.Report) {
	up := prometheus.NewDesc(namespace+"_backend_up",
		"Whether the check of a component or backend passed (1) or failed (0).", []string{"kind", "name"}, nil)
	latency := prometheus.NewDesc(namespace+"_backend_check_duration_seconds",
		"Duration of the last check of a component or backend.", []string{"kind", "name"}, nil)
	m.reg.MustRegister(collector{descs: []*prometheus.Desc{up, latency}, collect: func(ch chan<- prometheus.Metric) {
		ctx, cancel := context.WithTimeout(context.Background(), backendTimeout)
		defer cancel()
		for _, c := range run(ctx).Components {
			v := 0.0
			if c.Status == "ok" {
				v = 1
			}
			ch <- prometheus.MustNewConstMetric(up, prometheus.GaugeValue, v, c.Kind, c.Name)
			ch <- prometheus.MustNewConstMetric(latency, prometheus.GaugeValue,
				float64(c.LatencyMS)/1000, c.Kind, c.Name)
		}
	}})
}

// Handler serves the metrics. If token is set, requests must send it as a
// bearer token.
func (m *Metrics) Handler(token string) http.Handler {
	h := promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
	if token == "" {
		return h
	}
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		sum := sha256.Sum256([]byte(got))
		if !ok || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="dcdash metrics"`)
			httpx.WriteError(w, http.StatusUnauthorized, "unauthenticated", "a valid bearer token is required")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// collector is a prometheus.Collector from functions.
type collector struct {
	descs   []*prometheus.Desc
	collect func(chan<- prometheus.Metric)
}

func (c collector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range c.descs {
		ch <- d
	}
}

func (c collector) Collect(ch chan<- prometheus.Metric) { c.collect(ch) }

// routeKey stores the *string the router fills with the matched route pattern.
type routeKey struct{}

// Middleware counts requests. It must wrap all other handlers, so that
// requests rejected early (e.g. by the CSRF check) are counted too; Router
// must wrap the router to record the route pattern.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		route := new(string)
		r = r.WithContext(context.WithValue(r.Context(), routeKey{}, route))
		rec := &recorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		pattern := *route
		if pattern == "" {
			pattern = "unmatched"
		}
		m.requests.WithLabelValues(pattern, r.Method, strconv.Itoa(rec.code)).Inc()
		if !rec.hijacked {
			m.duration.WithLabelValues(pattern).Observe(time.Since(start).Seconds())
		}
	})
}

// Router records the route pattern the router matched. Patterns, unlike
// paths, have a bounded number of values, e.g.
// "GET /api/v1/dhcp/servers/{server}/leases".
func Router(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if p, ok := r.Context().Value(routeKey{}).(*string); ok {
			*p = r.Pattern
		}
	})
}

// recorder captures the status code. It supports hijacking for the console
// websockets and flushing.
type recorder struct {
	http.ResponseWriter
	code        int
	wroteHeader bool
	hijacked    bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.code, r.wroteHeader = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

func (r *recorder) Flush() { _ = http.NewResponseController(r.ResponseWriter).Flush() }

func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, rw, err := http.NewResponseController(r.ResponseWriter).Hijack()
	if err == nil {
		r.hijacked, r.code = true, http.StatusSwitchingProtocols
	}
	return c, rw, err
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
