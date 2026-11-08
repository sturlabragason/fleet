package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/web"
)

// cmdWeb runs `fleet web` in the foreground (DESIGN.md §5f, ADR-66).
func cmdWeb(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	listen := fs.String("listen", envOr("FLEET_WEB_LISTEN", "127.0.0.1:7777"), "address to listen on")
	hosts := fs.String("allow-host", os.Getenv("FLEET_WEB_HOSTS"), "extra host names (comma-separated) the browser may use, e.g. the gateway's")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if me := f.Whoami(""); me != "" {
		return fmt.Errorf("fleet web acts as the user; run it outside agent pane %q", me)
	}
	token, err := web.LoadToken(f.Home)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	s := web.New(f, token, *listen, strings.Split(*hosts, ","))
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		fmt.Fprintf(os.Stderr, "fleet web: listening on %s, not only on this machine: anyone who reaches it needs the token\n", ln.Addr())
		host = "localhost"
	}
	fmt.Printf("fleet web: http://%s/?token=%s\n", net.JoinHostPort(host, port), token)
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
