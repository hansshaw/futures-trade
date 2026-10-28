package main

import (
	"context"
	"cornwatch/internal/analysis"
	"cornwatch/internal/journal"
	"cornwatch/internal/server"
	"cornwatch/internal/store"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "listen address")
	data := flag.String("data", "data/cornwatch.db", "local bbolt database path")
	ui := flag.String("ui", "desktop/dist", "built renderer path")
	seed := flag.Bool("demo", true, "include isolated, explicitly synthetic demo data")
	flag.Parse()
	token := os.Getenv("CORNWATCH_TOKEN")
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatal(err)
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" && len(token) < 32 {
		log.Fatal("non-loopback listener requires CORNWATCH_TOKEN of at least 32 characters")
	}
	s, err := store.Open(*data)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	if *seed {
		if err = s.SeedDemo(); err != nil {
			log.Fatal(err)
		}
	}
	a, err := analysis.New(analysis.Config{BaseURL: os.Getenv("LLM_BASE_URL"), Model: os.Getenv("LLM_MODEL"), APIKey: os.Getenv("LLM_API_KEY")})
	if err != nil {
		log.Fatal(err)
	}
	// Independent journal storage keeps screenshots away from the market store;
	// both files live together and must be backed up together.
	j, err := journal.Open(*data+".journal.db", a.Config)
	if err != nil {
		log.Fatal(err)
	}
	defer j.Close()
	feeds := []string{}
	for _, f := range strings.Split(os.Getenv("CORNWATCH_FEED_URLS"), ",") {
		if f = strings.TrimSpace(f); f != "" {
			feeds = append(feeds, f)
		}
	}
	interval := 5 * time.Minute
	if value := os.Getenv("CORNWATCH_POLL_INTERVAL"); value != "" {
		interval, err = time.ParseDuration(value)
		if err != nil || interval < time.Minute {
			log.Fatal("CORNWATCH_POLL_INTERVAL must be a duration >=1m")
		}
	}
	app := server.New(s, a, server.Config{Token: token, UI: *ui, AllowedOrigin: os.Getenv("CORNWATCH_ALLOWED_ORIGIN"), FeedURLs: feeds, WeatherEnabled: os.Getenv("CORNWATCH_WEATHER_ENABLED") == "true", PollInterval: interval, Journal: j})
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	httpServer := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 * 1024, BaseContext: func(net.Listener) context.Context { return ctx }}
	go app.Poll(ctx)
	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("server: %v", err)
			cancel()
		}
	}()
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"event": "listening", "address": "http://" + listener.Addr().String()})
	<-ctx.Done()
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := httpServer.Shutdown(shutdown); err != nil {
		_ = httpServer.Close()
	}
}
