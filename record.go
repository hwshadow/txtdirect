/*
Copyright 2019 - The TXTDirect Authors
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
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Record struct {
	Version string
	To      string
	Code    int
	Type    string
	Use     []string
	Vcs     string
	Website string
	From    string
	Root    string
	Re      string
	Ref     bool
	Headers map[string]string
}

// GetRecord uses the given host to find a TXT record
// and then parses the txt record and returns a TXTDirect record
// struct instance. It returns an error when it can't find any txt
// records or if the TXT record is not standard.
func GetRecord(host string, c Config, w http.ResponseWriter, r *http.Request) (Record, error) {
	txts, err := query(host, r.Context(), c)
	if err != nil {
		log.Printf("Initial DNS query failed: %s", err)
	}

	// If record isn't on apex zone, check the "_" subzone
	if err != nil && r.Context().Value("records") == nil {
		txts, err = query(fmt.Sprintf("_.%s", host), r.Context(), c)
		if err != nil {
			log.Printf("Apex zone's wildcard DNS query failed: %s", err)
		}
	}

	// if error present or record empty, jump into wildcards
	if err != nil || txts[0] == "" {
		hostSlice := strings.Split(host, ".")
		hostSlice[0] = "_"
		host = strings.Join(hostSlice, ".")
		txts, err = query(host, r.Context(), c)
		if err != nil {
			log.Printf("Wildcard DNS query failed: %s", err.Error())
			return Record{}, err
		}
	}

	if len(txts) != 1 {
		return Record{}, fmt.Errorf("could not parse TXT record with %d records", len(txts))
	}

	var rec Record
	if rec, err = ParseRecord(txts[0], w, r, c); err != nil {
		return rec, fmt.Errorf("could not parse record: %s", err)
	}

	r = rec.addToContext(r)

	// Add the headers from record to the response
	if len(rec.Headers) != 0 {
		for header, val := range rec.Headers {
			w.Header().Set(header, val)
		}
	}

	return rec, nil
}

// ParseRecord takes a string containing the DNS TXT record and returns
// a TXTDirect record struct instance.
// It will return an error if the DNS TXT record is not standard or
// if the record type is not enabled in the TXTDirect's config.
func ParseRecord(str string, w http.ResponseWriter, req *http.Request, c Config) (Record, error) {
	r := Record{
		Headers: map[string]string{},
	}

	s := strings.Split(str, ";")

	// Trim whitespace both leading and trailing
	for i := range s {
		s[i] = strings.TrimSpace(s[i])
	}

	for _, l := range s {
		switch {
		case strings.HasPrefix(l, "code="):
			l = strings.TrimPrefix(l, "code=")
			i, err := strconv.Atoi(l)
			if err != nil {
				return Record{}, fmt.Errorf("could not parse status code: %s", err)
			}
			r.Code = i

		case strings.HasPrefix(l, "from="):
			l = strings.TrimPrefix(l, "from=")
			l, err := parsePlaceholders(l, req, []string{})
			if err != nil {
				return Record{}, err
			}
			r.From = l

		case strings.HasPrefix(l, "re="):
			l = strings.TrimPrefix(l, "re=")
			r.Re = l

		case strings.HasPrefix(l, "ref="):
			l, err := strconv.ParseBool(strings.TrimPrefix(l, "ref="))
			if err != nil {
				fallback(w, req, "global", http.StatusMovedPermanently, c)
				return Record{}, err
			}
			r.Ref = l

		case strings.HasPrefix(l, "root="):
			l = strings.TrimPrefix(l, "root=")
			l, err := parsePlaceholders(l, req, []string{})
			if err != nil {
				return Record{}, err
			}
			l = ParseURI(l, w, req, c)
			r.Root = l

		case strings.HasPrefix(l, "to="):
			l = strings.TrimPrefix(l, "to=")
			l, err := parsePlaceholders(l, req, []string{})
			if err != nil {
				return Record{}, err
			}
			l = ParseURI(l, w, req, c)
			r.To = l

		case strings.HasPrefix(l, "type="):
			l = strings.TrimPrefix(l, "type=")
			r.Type = l

		case strings.HasPrefix(l, "use="):
			l = strings.TrimPrefix(l, "use=")
			if !strings.HasPrefix(l, "_redirect.") {
				return Record{}, fmt.Errorf("The given zone address is invalid")
			}
			r.Use = append(r.Use, l)

		case strings.HasPrefix(l, "v="):
			l = strings.TrimPrefix(l, "v=")
			r.Version = l
			if r.Version != "txtv0" {
				return Record{}, fmt.Errorf("unhandled version '%s'", r.Version)
			}
			log.Print("WARN: txtv0 is not suitable for production")

		case strings.HasPrefix(l, "vcs="):
			l = strings.TrimPrefix(l, "vcs=")
			r.Vcs = l

		case strings.HasPrefix(l, "website="):
			l = strings.TrimPrefix(l, "website=")
			l = ParseURI(l, w, req, c)
			r.Website = l
		case strings.HasPrefix(l, ">"):
			header := strings.Split(l, "=")
			h, err := url.PathUnescape(header[1])
			if err != nil {
				return Record{}, err
			}
			r.Headers[header[0][1:]] = h
		default:
			tuple := strings.Split(l, "=")
			if len(tuple) != 2 {
				return Record{}, fmt.Errorf("arbitrary data not allowed")
			}
			continue
		}
		if len(l) > 255 {
			return Record{}, fmt.Errorf("TXT record cannot exceed the maximum of 255 characters")
		}
	}

	if r.Type == "dockerv2" && r.To == "" {
		return Record{}, fmt.Errorf("[txtdirect]: to= field is required in dockerv2 type")
	}

	if r.Code == 0 {
		r.Code = http.StatusFound
	}

	// Only apply rules and default to records that doesn't point to a upstream record
	if len(r.Use) == 0 {
		if r.Type == "" {
			r.Type = "host"
		}

		if r.Type == "host" && r.To == "" {
			fallback(w, req, "global", http.StatusMovedPermanently, c)
			return Record{}, nil
		}

		if !contains(c.Enable, r.Type) {
			return Record{}, fmt.Errorf("%s type is not enabled in configuration", r.Type)
		}
	}

	return r, nil
}

// Adds the given record to the request's context with "records" key.
func (rec Record) addToContext(r *http.Request) *http.Request {
	// Fetch fallback config from context and add the record to it
	recordsContext := r.Context().Value("records")

	// Create a new records field in the context if it doesn't exist
	if recordsContext == nil {
		return r.WithContext(context.WithValue(r.Context(), "records", []Record{rec}))
	}

	records := append(recordsContext.([]Record), rec)

	// Replace the fallback config instance inside the request's context
	return r.WithContext(context.WithValue(r.Context(), "records", records))
}

// UpstreamRecord will check all of the use= fields and sends a request to each
// upstream zone address and choses the first one that returns the final TXT
// record
func (rec *Record) UpstreamRecord(c Config, w http.ResponseWriter, r *http.Request) (Record, string, error) {
	var upstreamRec Record
	var err error

	for _, zone := range rec.Use {
		upstreamRec, err = GetRecord(zone, c, w, r)
		if err != nil {
			continue
		}
		return upstreamRec, zone, nil
	}

	return Record{}, "", fmt.Errorf("Couldn't find any records from upstream")
}

func (rec *Record) CheckUpstream(w http.ResponseWriter, r *http.Request, c Config) (*http.Request, error) {
	// Add the upstream zone address from the use= fields to the request context
	if len(rec.Use) != 0 {
		var zone string
		upstreamRec, zone, err := rec.UpstreamRecord(c, w, r)
		if err != nil {
			return r, err
		}

		*rec = upstreamRec

		zoneSplited := strings.Split(zone, ".")

		return r.WithContext(context.WithValue(
			r.Context(),
			"upstreamZone",
			strings.Join(zoneSplited[1:], "."),
		)), nil
	}
	return r, nil
}

// ParseURI parses the given URI and triggers fallback if the URI isn't valid
func ParseURI(uri string, w http.ResponseWriter, r *http.Request, c Config) string {
	url, err := url.Parse(uri)
	if err != nil {
		fallback(w, r, "global", http.StatusMovedPermanently, c)
		return ""
	}
	return url.String()
}

// getBaseTarget parses the placeholder in the given record's To= field
// and returns the final address and http status code
func getBaseTarget(rec Record, r *http.Request) (string, int, error) {
	if strings.ContainsAny(rec.To, "{}") {
		to, err := parsePlaceholders(rec.To, r, []string{})
		if err != nil {
			return "", 0, err
		}
		rec.To = to
	}
	return rec.To, rec.Code, nil
}

func absoluteZone(zone string) string {
	// Removes port from zone
	if strings.Contains(zone, ":") {
		zoneSlice := strings.Split(zone, ":")
		zone = zoneSlice[0]
	}

	if !strings.HasPrefix(zone, basezone) {
		zone = strings.Join([]string{basezone, zone}, ".")
	}

	if strings.HasSuffix(zone, ".") {
		return zone
	}

	return strings.Join([]string{zone, "."}, "")
}

// query checks the given zone using net.LookupTXT to
// find TXT records in that zone
func query(zone string, ctx context.Context, c Config) ([]string, error) {
	var txts []string
	var err error
	if c.Resolver != "" {
		net := customResolver(c)
		txts, err = net.LookupTXT(ctx, absoluteZone(zone))
	} else {
		txts, err = net.LookupTXT(absoluteZone(zone))
	}
	if err != nil {
		return nil, fmt.Errorf("could not get TXT record: %s", err)
	}
	if txts[0] == "" {
		return nil, fmt.Errorf("TXT record doesn't exist or is empty")
	}
	return txts, nil
}
