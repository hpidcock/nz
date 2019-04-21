package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"golang.org/x/net/http2"

	jsonDNS "github.com/m13253/dns-over-https/json-dns"

	dns "github.com/miekg/dns"
)

var (
	errBadStatus = errors.New("bad status")
)

var (
	flagListen       = flag.String("listen", "127.0.0.2:53", "internal dns listen address")
	flagAllowCookies = flag.Bool("allow-cookies", true, "allow cookies to be stored in the DNS-over-HTTPs client")
	flagTimeout      = flag.Duration("timeout", 5*time.Second, "timeout on upstream https requests")
	flagUpstream     = flag.String("upstream", "https://1.1.1.1/dns-query,https://dns.google.com/resolve",
		"comma delimited list of upstream DNS-over-HTTPs resolvers")
)

var upstream = []string{}
var client *http.Client

func main() {
	var err error
	flag.Parse()

	upstream = strings.Split(*flagUpstream, ",")

	var cookies http.CookieJar
	if *flagAllowCookies {
		cookies, _ = cookiejar.New(nil)
	}

	tlsConfig := &tls.Config{
		MinVersion:               tls.VersionTLS12,
		MaxVersion:               tls.VersionTLS13,
		PreferServerCipherSuites: true,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
		},
	}

	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return nil, io.ErrClosedPipe
		},
		Dial: func(_, _ string) (net.Conn, error) {
			return nil, io.ErrClosedPipe
		},
		DialTLS: func(network, addr string) (net.Conn, error) {
			return tls.Dial(network, addr, tlsConfig)
		},
	}

	err = http2.ConfigureTransport(transport)
	if err != nil {
		log.Fatal(err)
	}

	client = &http.Client{
		Timeout: *flagTimeout,
		Jar:     cookies,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: transport,
	}

	dns.HandleFunc(".", handleDNSRequest)
	nss := dns.Server{Addr: *flagListen, Net: "udp"}
	err = nss.ListenAndServe()
	if err != nil {
		log.Fatal(err)
	}
}

func doh(remote string, r *dns.Msg) (*dns.Msg, error) {
	q := r.Question[0]

	u := fmt.Sprintf("%s?name=%s&type=%d", remote, q.Name, q.Qtype)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("accept", "application/dns-json")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, errBadStatus
	}

	dnsJSONRes := jsonDNS.Response{}
	dec := json.NewDecoder(res.Body)
	err = dec.Decode(&dnsJSONRes)
	if err != nil {
		return nil, err
	}

	reply := jsonDNS.PrepareReply(r)
	reply = jsonDNS.Unmarshal(reply, &dnsJSONRes, dns.DefaultMsgSize, 255)
	return reply, nil
}

func handleDNSRequest(w dns.ResponseWriter, r *dns.Msg) {
	log.Printf("got question from %s\n", w.RemoteAddr().String())

	if r.Response {
		log.Print("got response message as request")
		w.Close()
		return
	}

	if len(r.Question) != 1 {
		log.Printf("got message for %d questions\n", len(r.Question))
		w.Close()
		return
	}

	type response struct {
		err error
		msg *dns.Msg
	}
	rc := make(chan response, len(upstream))

	for _, v := range upstream {
		remote := v
		go func() {
			res, err := doh(remote, r)
			rc <- response{err, res}
		}()
	}

	for range upstream {
		res := <-rc
		if res.err != nil {
			log.Print(res.err)
			continue
		}

		w.WriteMsg(res.msg)
		return
	}

	w.Close()
}
