// Package checks runs the checks: probes for each type, a scheduler, and
// the state machine that turns results into up/down, incidents and
// notifications.
package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/lookout/internal/store"
)

// Outcome is one probe's answer.
type Outcome struct {
	OK      bool
	Latency time.Duration
	Message string
	// CertExpires is the served certificate's expiry, for HTTPS and TLS.
	CertExpires time.Time
	// Asleep: Gatehouse stopped the app on purpose (scale-to-zero). It's
	// neither up nor down, and doesn't go in the history.
	Asleep bool
}

// Gatehouse doesn't wake an app, or count it as activity, for requests
// with ProbeHeader; a sleeping app answers 503 with StateHeader.
const (
	ProbeHeader = "X-Gatehouse-Probe"
	StateHeader = "X-Gatehouse-State"
)

func asleep(start time.Time, state string) Outcome {
	return Outcome{Latency: time.Since(start), Asleep: true, Message: "Asleep: stopped by Gatehouse until it's used (" + state + ")"}
}

func fail(start time.Time, format string, args ...any) Outcome {
	return Outcome{Latency: time.Since(start), Message: fmt.Sprintf(format, args...)}
}

// Docker is the client for docker checks; nil when there's no socket.
var Docker *DockerClient

// Probe runs a check once.
func Probe(ctx context.Context, c store.Check) Outcome {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.Timeout)*time.Second)
	defer cancel()
	switch c.Type {
	case store.HTTP:
		return probeHTTP(ctx, c)
	case store.TCP:
		return probeTCP(ctx, c)
	case store.Ping:
		return probePing(ctx, c)
	case store.DNS:
		return probeDNS(ctx, c)
	case store.Docker:
		return probeDocker(ctx, c)
	case store.TLS:
		return probeTLS(ctx, c)
	}
	return Outcome{Message: "unknown check type " + c.Type}
}

const userAgent = "Lookout/1 (+https://github.com/audemed44/lookout)"

// Two transports, so certificate checking is per check. No keep-alives:
// each run makes a fresh connection, so it sees the certificate actually
// served now, and nothing stays open between runs.
var (
	verified   = &http.Transport{Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second}
	unverified = &http.Transport{Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
)

func probeHTTP(ctx context.Context, c store.Check) Outcome {
	start := time.Now()
	tr := verified
	if c.IgnoreTLS {
		tr = unverified
	}
	client := &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("more than 10 redirects")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, c.Method, c.Target, nil)
	if err != nil {
		return fail(start, "invalid URL: %v", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set(ProbeHeader, "1")
	resp, err := client.Do(req)
	if err != nil {
		return fail(start, "%s", describeHTTPError(err))
	}
	defer resp.Body.Close()
	if st := resp.Header.Get(StateHeader); st != "" && resp.StatusCode == http.StatusServiceUnavailable {
		return asleep(start, st)
	}
	out := Outcome{Latency: time.Since(start)}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		out.CertExpires = resp.TLS.PeerCertificates[0].NotAfter
	}
	if !StatusAccepted(c.Status, resp.StatusCode) {
		out.Message = fmt.Sprintf("HTTP %d (expected %s)", resp.StatusCode, c.Status)
		return out
	}
	out.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
	if c.Keyword == "" && c.JSONPath == "" {
		out.OK = true
		return out
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		out.Message = "could not read the response: " + err.Error()
		return out
	}
	out.Latency = time.Since(start)
	if c.Keyword != "" {
		found := strings.Contains(string(body), c.Keyword)
		if found == c.InvertKeyword {
			if c.InvertKeyword {
				out.Message = fmt.Sprintf("found %q", c.Keyword)
			} else {
				out.Message = fmt.Sprintf("%q not found", c.Keyword)
			}
			return out
		}
	}
	if c.JSONPath != "" {
		var doc any
		if err := json.Unmarshal(body, &doc); err != nil {
			out.Message = "response isn't JSON"
			return out
		}
		v, ok := JSONLookup(doc, c.JSONPath)
		if !ok {
			out.Message = c.JSONPath + " not found"
			return out
		}
		if c.Expect != "" && v != c.Expect {
			out.Message = fmt.Sprintf("%s is %q, expected %q", c.JSONPath, v, c.Expect)
			return out
		}
		out.Message += " · " + c.JSONPath + " = " + v
	}
	out.OK = true
	return out
}

func describeHTTPError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var cert x509.CertificateInvalidError
	var host x509.HostnameError
	var unknown x509.UnknownAuthorityError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.As(err, &cert):
		if cert.Reason == x509.Expired {
			return "certificate expired: " + cert.Detail
		}
		return "invalid certificate: " + cert.Error()
	case errors.As(err, &host):
		return "certificate doesn't match the host: " + host.Error()
	case errors.As(err, &unknown):
		return "certificate signed by an unknown authority"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return "no such host " + dnsErr.Name
		}
		return "DNS: " + dnsErr.Error()
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Op + ": " + op.Err.Error()
	}
	return err.Error()
}

// StatusAccepted reports whether code is in a list like "200-299,401".
func StatusAccepted(spec string, code int) bool {
	if strings.TrimSpace(spec) == "" {
		spec = "200-299"
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				continue
			}
		}
		if code >= a && code <= b {
			return true
		}
	}
	return false
}

// JSONLookup follows a path like "data.items[0].status" (a leading "$."
// is allowed) and returns the value as text.
func JSONLookup(doc any, path string) (string, bool) {
	path = strings.TrimPrefix(strings.TrimPrefix(path, "$"), ".")
	cur := doc
	for _, seg := range splitPath(path) {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[seg]
			if !ok {
				return "", false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(v) {
				return "", false
			}
			cur = v[i]
		default:
			return "", false
		}
	}
	switch v := cur.(type) {
	case string:
		return v, true
	case nil:
		return "null", true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(v), true
	}
	raw, _ := json.Marshal(cur)
	return string(raw), true
}

func splitPath(p string) []string {
	var out []string
	for _, part := range strings.Split(p, ".") {
		for part != "" {
			i := strings.IndexByte(part, '[')
			if i < 0 {
				out = append(out, part)
				break
			}
			if i > 0 {
				out = append(out, part[:i])
			}
			j := strings.IndexByte(part, ']')
			if j < i {
				out = append(out, part[i+1:])
				break
			}
			out = append(out, part[i+1:j])
			part = part[j+1:]
		}
	}
	return out
}

func probeTCP(ctx context.Context, c store.Check) Outcome {
	start := time.Now()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.Target)
	if err != nil {
		return fail(start, "%s", describeHTTPError(err))
	}
	conn.Close()
	return Outcome{OK: true, Latency: time.Since(start), Message: "port open"}
}

func probeTLS(ctx context.Context, c store.Check) Outcome {
	start := time.Now()
	addr := c.Target
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(strings.Trim(addr, "[]"), "443")
	}
	host, _, _ := net.SplitHostPort(addr)
	d := tls.Dialer{Config: &tls.Config{ServerName: host, InsecureSkipVerify: true}}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fail(start, "%s", describeHTTPError(err))
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	out := Outcome{Latency: time.Since(start)}
	if len(state.PeerCertificates) == 0 {
		out.Message = "no certificate"
		return out
	}
	leaf := state.PeerCertificates[0]
	out.CertExpires = leaf.NotAfter
	if !c.IgnoreTLS {
		inter := x509.NewCertPool()
		for _, ic := range state.PeerCertificates[1:] {
			inter.AddCert(ic)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter}); err != nil {
			out.Message = describeHTTPError(err)
			return out
		}
	}
	out.OK = true
	out.Message = fmt.Sprintf("certificate valid until %s", leaf.NotAfter.Format("2 Jan 2006"))
	return out
}

func probeDNS(ctx context.Context, c store.Check) Outcome {
	start := time.Now()
	r := net.DefaultResolver
	if c.Resolver != "" {
		server := c.Resolver
		if _, _, err := net.SplitHostPort(server); err != nil {
			server = net.JoinHostPort(strings.Trim(server, "[]"), "53")
		}
		r = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, server)
		}}
	}
	name := strings.TrimSuffix(c.Target, ".")
	var answers []string
	var err error
	switch strings.ToUpper(c.Record) {
	case "A", "AAAA":
		var ips []net.IP
		network := "ip4"
		if strings.ToUpper(c.Record) == "AAAA" {
			network = "ip6"
		}
		ips, err = r.LookupIP(ctx, network, name)
		for _, ip := range ips {
			answers = append(answers, ip.String())
		}
	case "CNAME":
		var cname string
		cname, err = r.LookupCNAME(ctx, name)
		answers = append(answers, strings.TrimSuffix(cname, "."))
	case "MX":
		var mx []*net.MX
		mx, err = r.LookupMX(ctx, name)
		for _, m := range mx {
			answers = append(answers, strings.TrimSuffix(m.Host, "."))
		}
	case "TXT":
		answers, err = r.LookupTXT(ctx, name)
	case "NS":
		var ns []*net.NS
		ns, err = r.LookupNS(ctx, name)
		for _, n := range ns {
			answers = append(answers, strings.TrimSuffix(n.Host, "."))
		}
	default:
		return fail(start, "unsupported record type %s", c.Record)
	}
	if err != nil {
		return fail(start, "%s", describeHTTPError(err))
	}
	if len(answers) == 0 {
		return fail(start, "no %s records", c.Record)
	}
	out := Outcome{Latency: time.Since(start), Message: strings.Join(answers, ", ")}
	if c.Expect != "" {
		for _, a := range answers {
			if strings.EqualFold(a, c.Expect) {
				out.OK = true
				return out
			}
		}
		out.Message = fmt.Sprintf("got %s, expected %s", out.Message, c.Expect)
		return out
	}
	out.OK = true
	return out
}

func probeDocker(ctx context.Context, c store.Check) Outcome {
	start := time.Now()
	if Docker == nil {
		return fail(start, "no Docker socket mounted")
	}
	st, err := Docker.State(ctx, c.Target)
	if err != nil {
		return fail(start, "%v", err)
	}
	out := Outcome{Latency: time.Since(start), Message: st.Status}
	if st.Health != "" {
		out.Message += " · " + st.Health
	}
	out.OK = st.Status == "running" && st.Health != "unhealthy"
	return out
}
