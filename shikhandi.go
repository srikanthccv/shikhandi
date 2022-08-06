package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"net/http"
	_ "net/http/pprof" // http profiler
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/file"
	flag "github.com/spf13/pflag"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"

	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type RootRoute struct {
	Service       string `koanf:"service"`
	Route         string `koanf:"route"`
	TracerPerHour int    `koanf:"tracesPerHour"`
}

type Topology struct {
	Services []Service `koanf:"services"`
}

type Service struct {
	ServiceName   string         `koanf:"serviceName"`
	Instances     []string       `koanf:"instances"`
	AttributeSets []AttributeSet `koanf:"attributeSets"`
	EventSets     []EventSet     `koanf:"eventSets"`
	SpanKind      string         `koanf:"spanKind"`
	ServiceRoutes []ServiceRoute `koanf:"routes"`
}

type AttributeSet struct {
	Weight     int                    `koanf:"weight"`
	Attributes map[string]interface{} `koanf:"attributes"`
}

type EventSet struct {
	Weight int     `koanf:"weight"`
	Events []Event `koanf:"events"`
}

type Event struct {
	Name       string                 `koanf:"name"`
	Attributes map[string]interface{} `koanf:"attributes"`
	Timestamp  int                    `koanf:"timestamp"`
}

type ServiceRoute struct {
	Route            string            `koanf:"route"`
	DownstreamRoutes map[string]string `koanf:"downstreamCalls"`
	AttributeSets    []AttributeSet    `koanf:"attributeSets"`
	EventSets        []EventSet        `koanf:"eventSets"`
	SpanKind         string            `koanf:"spanKind"`
	MaxLatencyMillis int               `koanf:"maxLatencyMillis"`
}

type CustomSampler struct{}

func (cs CustomSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	r := inRange(1, 4)
	decision := sdktrace.RecordAndSample
	if r%3 == 0 {
		decision = sdktrace.RecordOnly
		log.Printf("Dropping span: %s for trace %s\n", trace.SpanContextFromContext(p.ParentContext).SpanID(), trace.SpanContextFromContext(p.ParentContext).TraceID())
	}
	return sdktrace.SamplingResult{
		Decision:   decision,
		Attributes: p.Attributes,
		Tracestate: trace.SpanContextFromContext(p.ParentContext).TraceState(),
	}
}

func (cs CustomSampler) Description() string {
	return "CustomSampler"
}

var k = koanf.New(".")
var stp map[string]trace.Tracer
var t Topology

// Returns random integer b/w a-b
func inRange(x, y int) int {
	r, _ := rand.Int(rand.Reader, big.NewInt(int64(y-x)))
	return x + int(r.Int64())
}

// Weighted random selection of attribute set using cumulative density frequency
func pickAttributeSet(a *[]AttributeSet) int { // simple brute force approach; improve? maybe?
	cumulative := 0
	for _, attributeSet := range *(a) {
		cumulative += attributeSet.Weight
	}
	if cumulative == 0 {
		return -1
	}
	r := inRange(0, cumulative)
	for idx, attributeSet := range *(a) {
		r -= attributeSet.Weight
		if r < 0 {
			return idx
		}
	}
	return -1
}

func parseAttributes(s map[string]interface{}) []attribute.KeyValue {
	var r []attribute.KeyValue
	for key, value := range s {
		if val, ok := value.(int); ok {
			r = append(r, attribute.Int(key, val))
		} else if val, ok := value.(int64); ok {
			r = append(r, attribute.Int64(key, val))
		} else if val, ok := value.(string); ok {
			r = append(r, attribute.String(key, val))
		} else if val, ok := value.(bool); ok {
			r = append(r, attribute.Bool(key, val))
		} else if val, ok := value.(float64); ok {
			r = append(r, attribute.Float64(key, val))
		} else if val, ok := value.([]int); ok {
			r = append(r, attribute.IntSlice(key, val))
		} else if val, ok := value.([]int64); ok {
			r = append(r, attribute.Int64Slice(key, val))
		} else if val, ok := value.([]string); ok {
			r = append(r, attribute.StringSlice(key, val))
		} else if val, ok := value.([]bool); ok {
			r = append(r, attribute.BoolSlice(key, val))
		} else if val, ok := value.([]float64); ok {
			r = append(r, attribute.Float64Slice(key, val))
		}
	}
	return r
}

func setAttributesForSpan(s *trace.Span, a *[]AttributeSet) {
	idx := pickAttributeSet(a)
	if idx == -1 {
		return
	}
	selectedSet := (*(a))[idx]
	(*s).SetAttributes(parseAttributes(selectedSet.Attributes)...)
}

// Weighted random selection of event set using cumulative density frequency
func pickEventSet(e *[]EventSet) int {
	cumulative := 0
	for _, eventSet := range *(e) {
		cumulative += eventSet.Weight
	}
	if cumulative == 0 {
		return -1
	}
	r := inRange(0, cumulative)
	for idx, eventSet := range *(e) {
		r -= eventSet.Weight
		if r < 0 {
			return idx
		}
	}
	return -1
}

func getSpanKind(s string) trace.SpanKind {
	lower := strings.ToLower(s)
	switch lower {
	case "internal", "":
		return trace.SpanKindInternal
	case "consumer":
		return trace.SpanKindConsumer
	case "producer":
		return trace.SpanKindProducer
	case "client":
		return trace.SpanKindClient
	case "server":
		return trace.SpanKindServer
	default:
		return trace.SpanKindInternal
	}
}

func addEventsForSpan(s *trace.Span, e *[]EventSet) {
	p := pickEventSet(e)
	if p == -1 {
		return
	}
	pickedEventSet := (*(e))[p]
	for _, event := range pickedEventSet.Events {
		attrs := parseAttributes(event.Attributes)
		timestamp := time.Now()
		if event.Timestamp != 0 {
			timestamp = time.Unix(0, int64(event.Timestamp))
		}
		(*s).AddEvent(event.Name, trace.WithAttributes(attrs...), trace.WithTimestamp(timestamp))
	}
}

func emitTrace(serviceName string, routeName string, ctx context.Context) {
	for _, s := range t.Services {
		if s.ServiceName == serviceName {
			// start span
			var span trace.Span
			var parentContext context.Context
			kind := getSpanKind(s.SpanKind)
			parentContext, span = stp[serviceName].Start(
				ctx, routeName, trace.WithSpanKind(kind),
			)
			if !trace.SpanContextFromContext(ctx).IsValid() {
				log.Printf("Started new trace: %s\n", span.SpanContext().TraceID())
			}
			// set unique instance identifier
			n := inRange(0, len(s.Instances))
			span.SetAttributes(attribute.String("service.instance.id", s.Instances[n]))
			// pick an attribute set and apply
			setAttributesForSpan(&span, &s.AttributeSets)
			// pick an event set and apply
			addEventsForSpan(&span, &s.EventSets)
			endTime := time.Now()
			for _, r := range s.ServiceRoutes {
				if r.Route == routeName {
					// pick an attribute set from route and apply config
					setAttributesForSpan(&span, &r.AttributeSets)
					// pick an event set from route and apply config
					addEventsForSpan(&span, &r.EventSets)
					for dService, dRoute := range r.DownstreamRoutes {
						// launch new request for all the downstream services
						go emitTrace(dService, dRoute, parentContext)
					}
					endTime = endTime.Add(time.Millisecond * time.Duration(int64(inRange(0, r.MaxLatencyMillis-1))))
					break
				}
			}
			span.End(trace.WithTimestamp(endTime))
			break
		}
	}
}

func generate(rootRoute RootRoute, quit <-chan bool) {
	ticker := time.NewTicker(
		time.Duration(time.Hour.Milliseconds()/int64(rootRoute.TracerPerHour)) * time.Millisecond,
	)
	for {
		select {
		case <-ticker.C:
			go emitTrace(rootRoute.Service, rootRoute.Route, context.Background())
		case <-quit:
			ticker.Stop()
			return
		}
	}
}

func startPprofServer(pprofAddress string) {
	go func() {
		log.Println("Starting pprof server", pprofAddress)

		err := http.ListenAndServe(pprofAddress, nil)
		if err != nil {
			log.Fatalf("could not start pprof server: %v", err)
		}

		log.Println("pprof server started", pprofAddress)
	}()
}

func main() {
	// Command line args
	f := flag.NewFlagSet("config", flag.ExitOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}

	f.String("topologyFile", "", "File describing the anatomy")
	f.String("serviceNamespace", "shikandi", "Set OtelCollector resource attribute: service.namespace")
	f.String("pprofAddress", "0.0.0.0:6060", "Address of pprof server")
	err := f.Parse(os.Args[1:])
	if err != nil {
		log.Fatalf("Failed to parse args %v", err)
	}

	tFile, _ := f.GetString("topologyFile")
	serviceNamespace, _ := f.GetString("serviceNamespace")
	pprofAddress, _ := f.GetString("pprofAddress")

	if err := k.Load(file.Provider(tFile), json.Parser()); err != nil {
		log.Fatalf("error loading topology file: %v", err)
	}

	startPprofServer(pprofAddress)

	var rootRoutes []RootRoute
	err = k.Unmarshal("topology", &t)
	if err != nil {
		log.Fatalf("Failed to Unmarshal topology %v", err)
	}
	err = k.Unmarshal("rootRoutes", &rootRoutes)
	if err != nil {
		log.Fatalf("Failed to Unmarshal rootRoutes %v", err)
	}
	stp = make(map[string]trace.Tracer)

	log.Println("Starting pipeline, this may take a while...")
	for _, service := range t.Services {
		ctx := context.Background()
		serviceResource, err := resource.New(ctx,
			resource.WithAttributes(
				attribute.String("service.namespace", serviceNamespace),
				attribute.String("service.name", service.ServiceName),
			),
		)
		handleErr(err, "failed to create resource")
		traceExporter, err := otlptracegrpc.New(ctx)
		handleErr(err, "failed to create trace exporter")

		provider := sdktrace.NewTracerProvider(
			sdktrace.WithSampler(CustomSampler{}),
			sdktrace.WithResource(serviceResource),
			sdktrace.WithBatcher(traceExporter),
		)
		stp[service.ServiceName] = provider.Tracer("load-generator")
		defer func() {
			handleErr(provider.Shutdown(ctx), "failed to shutdown tracer provider")
		}()
	}
	fmt.Print("Starting load generator\n")

	quit := make(chan bool)
	exit := make(chan os.Signal, 1)
	signal.Notify(exit, os.Interrupt)

	// generation goes brrrrrr
	for _, rootRoute := range rootRoutes {
		go generate(rootRoute, quit)
	}

	<-exit
	log.Println("Shutting down...")
	quit <- true
}

func handleErr(err error, message string) {
	if err != nil {
		log.Fatalf("%s: %v", message, err)
	}
}
