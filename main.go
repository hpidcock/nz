package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"time"

	jsonDNS "github.com/m13253/dns-over-https/json-dns"

	dns "github.com/miekg/dns"
)

var upstream = []string{
	"https://1.1.1.1/dns-query",
	"https://dns.google.com/resolve",
}

var errBadStatus = errors.New("bad status")

var client *http.Client

func main() {
	cookies, _ := cookiejar.New(nil)
	client = &http.Client{
		Timeout: 5 * time.Second,
		Jar:     cookies,
	}

	dns.HandleFunc(".", handleDNSRequest)
	nss := dns.Server{Addr: "127.0.0.2:53", Net: "udp"}
	err := nss.ListenAndServe()
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
