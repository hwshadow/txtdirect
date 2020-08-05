/*
Copyright 2017 - The TXTDirect Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package txtdirect

import (
	"context"
	"log"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

// Testing TXT records
var txts = map[string]string{
	// type=host
	"_redirect.host.host.example.com.": "v=txtv0;to=https://plain.host.test;type=host;ref=true;>TestHeader=TestValue;code=302",

	// type=path
	"_redirect.path.path.example.com.": "v=txtv0;type=path;>TestHeader=TestValue;>TestHeader1=TestValue1",
	"_redirect.host.path.example.com.": "v=txtv0;type=host;to=https://host.host.example.com;",

	// query() function test records
	"_redirect.about.host.host.example.com.":   "v=txtv0;to=https://about.txtdirect.org",
	"_redirect.pkg.gometa.gometa.example.com.": "v=txtv0;to=https://pkg.txtdirect.org;type=gometa",
}

// Testing DNS server port
const port = 6000

// Initialize dns server instance
var server = &dns.Server{Addr: ":" + strconv.Itoa(port), Net: "udp"}

func TestMain(m *testing.M) {
	go RunDNSServer()
	os.Exit(m.Run())
}

func TestRedirectBlacklist(t *testing.T) {
	config := Config{
		Enable: []string{"path"},
	}
	req := httptest.NewRequest("GET", "https://txtdirect.com/favicon.ico", nil)
	w := httptest.NewRecorder()

	err := Redirect(w, req, config)
	if err != nil {
		t.Errorf("Unexpected error: %s", err)
	}
}

func Test_query(t *testing.T) {
	tests := []struct {
		zone string
		txt  string
	}{
		{
			"_redirect.about.host.host.example.com.",
			txts["_redirect.about.host.host.example.com."],
		},
		{
			"_redirect.pkg.gometa.gometa.example.com.",
			txts["_redirect.pkg.gometa.gometa.example.com."],
		},
	}
	for _, test := range tests {
		ctx := context.Background()
		c := Config{
			Resolver: "127.0.0.1:" + strconv.Itoa(port),
		}
		resp, err := query(test.zone, ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if resp[0] != txts[test.zone] {
			t.Fatalf("Expected %s, got %s", txts[test.zone], resp[0])
		}
	}
}

func parseDNSQuery(m *dns.Msg) {
	for _, q := range m.Question {
		switch q.Qtype {
		case dns.TypeTXT:
			log.Printf("Query for %s\n", q.Name)
			m.Answer = append(m.Answer, &dns.TXT{
				Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 60},
				Txt: []string{txts[q.Name]},
			})
		}
	}
}

func handleDNSRequest(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Compress = false

	switch r.Opcode {
	case dns.OpcodeQuery:
		parseDNSQuery(m)
	}

	w.WriteMsg(m)
}

func RunDNSServer() {
	dns.HandleFunc("example.com.", handleDNSRequest)
	err := server.ListenAndServe()
	defer server.Shutdown()
	if err != nil {
		log.Printf("Failed to start server: %s\n ", err.Error())
	}
}

func TestRedirectE2e(t *testing.T) {
	tests := []struct {
		url      string
		expected string
		enable   []string
		referer  bool
	}{
		{
			url:      "https://127.0.0.1/test",
			expected: "404",
			enable:   []string{"host"},
		},
		{
			url:      "https://192.168.1.2",
			expected: "404",
			enable:   []string{"host"},
		},
		{
			url:      "https://2001:db8:1234:0000:0000:0000:0000:0000",
			expected: "404",
			enable:   []string{"host"},
		},
		{
			url:      "https://2001:db8:1234::/48",
			expected: "404",
			enable:   []string{"host"},
		},
	}
	for _, test := range tests {
		req := httptest.NewRequest("GET", test.url, nil)
		resp := httptest.NewRecorder()
		c := Config{
			Resolver: "127.0.0.1:" + strconv.Itoa(port),
			Enable:   test.enable,
		}
		if err := Redirect(resp, req, c); err != nil {
			t.Errorf("Unexpected error occured: %s", err.Error())
		}
		if !strings.Contains(resp.Body.String(), test.expected) {
			t.Errorf("Expected %s to be in \"%s\"", test.expected, resp.Body.String())
		}
		if test.referer && resp.Header().Get("Referer") != req.Host {
			t.Errorf("Expected %s referer but got \"%s\"", req.Host, resp.Header().Get("Referer"))
		}
	}
}

func TestConfigE2e(t *testing.T) {
	tests := []struct {
		url    string
		txt    string
		enable []string
	}{
		{
			"https://e2e.txtdirect",
			txts["_redirect.path.txtdirect."],
			[]string{},
		},
		{
			"https://path.txtdirect/test",
			txts["_redirect.path.e2e.txtdirect."],
			[]string{"host"},
		},
		{
			"https://gometa.txtdirect",
			txts["_redirect.gometa.txtdirect."],
			[]string{"host"},
		},
	}
	for _, test := range tests {
		req := httptest.NewRequest("GET", test.url, nil)
		resp := httptest.NewRecorder()
		c := Config{
			Resolver: "127.0.0.1:" + strconv.Itoa(port),
			Redirect: "https://txtdirect.org",
		}
		Redirect(resp, req, c)
		if resp.Header().Get("Location") != c.Redirect {
			t.Errorf("Request didn't redirect to the specified URI after failure")
		}
	}
}

func Test_isIP(t *testing.T) {
	tests := []struct {
		host     string
		expected bool
	}{
		{
			"https://example.test",
			false,
		},
		{
			"http://example.test",
			false,
		},
		{
			"http://192.168.test.subdomain.test",
			false,
		},
		{
			"192.168.1.1",
			true,
		},
		{
			"https://122.221.122.221",
			true,
		},
		{
			"FE80:0000:0000:0000:0202:B3FF:FE1E:8329",
			true,
		},
		{
			"FE80::0202:B3FF:FE1E:8329",
			true,
		},
	}
	for _, test := range tests {
		if result := isIP(test.host); result != test.expected {
			t.Errorf("%s is an IP not a domain", test.host)
		}
	}
}

func Test_customResolver(t *testing.T) {
	tests := []struct {
		config Config
	}{
		{
			Config{
				Resolver: "127.0.0.1",
			},
		},
		{
			Config{
				Resolver: "8.8.8.8",
			},
		},
	}
	for _, test := range tests {
		resolver := customResolver(test.config)
		if resolver.PreferGo != true {
			t.Errorf("Expected PreferGo option to be enabled in the returned resolver")
		}
	}
}

func Test_contains(t *testing.T) {
	tests := []struct {
		array    []string
		word     string
		expected bool
	}{
		{
			[]string{"test", "txtdirect"},
			"test",
			true,
		},
		{
			[]string{"test", "txtdirect", "contains"},
			"txtdirect",
			true,
		},
		{
			[]string{"test", "txtdirect", "random"},
			"contains",
			false,
		},
	}
	for _, test := range tests {
		if result := contains(test.array, test.word); result != test.expected {
			t.Errorf("Expected %t but got %t.\nArray: %v \nWord: %v", test.expected, result, test.array, test.word)
		}
	}
}

func Test_getBaseTarget(t *testing.T) {
	tests := []struct {
		record Record
		reqURL string
		url    string
		status int
	}{
		{
			Record{
				To:   "https://example.test",
				Code: 200,
			},
			"https://nowhere.test",
			"https://example.test",
			200,
		},
		{
			Record{
				To:   "https://{host}/{method}",
				Code: 200,
			},
			"https://somewhere.test",
			"https://somewhere.test/GET",
			200,
		},
		{
			Record{
				To:   "https://testing.test{path}",
				Code: 301,
			},
			"https://example.test/testing/path",
			"https://testing.test/testing/path",
			301,
		},
	}
	for _, test := range tests {
		req := httptest.NewRequest("GET", test.reqURL, nil)
		to, status, err := getBaseTarget(test.record, req)
		if err != nil {
			t.Errorf("Expected the err to be nil but got %s", err)
		}
		if to != test.url {
			t.Errorf("Expected %s but got %s", test.url, to)
		}
		if err != nil {
			t.Errorf("Expected %d but got %d", test.status, status)
		}
	}
}
