package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// routeUnmatched 是没匹配上任何路由时用的 route 标签值。
//
// 必须用固定值：直接把 URL 当标签，随便扫一遍站点就能把指标基数打爆。
const routeUnmatched = "unmatched"

// Metrics 是 Prometheus 指标收集器（DECISIONS.md 只上 Prometheus，不上 OpenTelemetry）。
type Metrics struct {
	registry  *prometheus.Registry
	requests  *prometheus.CounterVec
	durations *prometheus.HistogramVec
	inFlight  prometheus.Gauge
}

// NewMetrics 造一个独立 registry 的指标收集器（不往全局默认 registry 里塞东西）。
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Metrics{
		registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP 请求总数",
		}, []string{"method", "route", "status"}),
		durations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP 请求耗时",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"method", "route"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "正在处理中的 HTTP 请求数",
		}),
	}
	reg.MustRegister(m.requests, m.durations, m.inFlight)
	return m
}

// Handler 是 /metrics 的处理器。
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Registry 暴露 registry，方便别的包注册自己的指标。
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// Middleware 统计请求数与耗时。
func (m *Metrics) Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			m.inFlight.Inc()
			// 用 defer：万一 panic 穿了过去，这个计数器也不会越涨越高。
			defer m.inFlight.Dec()
			start := time.Now()

			err := next(c)

			route := c.Path()
			if route == "" {
				route = routeUnmatched
			}
			method := c.Request().Method
			_, status := echo.ResolveResponseStatus(c.Response(), err)

			m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
			m.durations.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
			return err
		}
	}
}
