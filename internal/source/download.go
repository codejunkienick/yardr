package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// lookupIPs resolves a redirected hop's host for the check made before the
// hop is dialled. It is a variable so a test can say what a name resolves
// to without a name server, as the dial hook below is the seam at the
// connection itself.
var lookupIPs = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// DoFile makes the file call name of d and writes its answer to dest, a
// file the caller names, under maxBytes counted on the stream (no bound
// where maxBytes is not positive). It is the download path beside Do: Do
// itself is unchanged and still refuses every 3xx, while a call that names
// answer.file is answered here. The request is made as Do makes it, with
// the credential on the first request alone, and each answer is classed in
// the design's order: a 2xx is the file's bytes with no JSON refusal, a
// 3xx that may be followed is an intermediate answer and never a success,
// and anything else is a terminal refusal keeping at most answer.errors.
// The file is written beside dest and renamed only when complete, so dest
// never stands with partial bytes. readSecret is Do's.
func DoFile(ctx context.Context, d *Declaration, name string, params map[string]string, credential, dest string, maxBytes int64, readSecret func(path string) (secret []byte, resolved string, err error)) (*Result, error) {
	return doFile(ctx, d, name, params, credential, nil, nil, dest, maxBytes, readSecret)
}

// DoFileWithInputs is DoFile with file-backed inputs already read by the
// caller, as DoKeyedWithInputs is to DoKeyed. readSecret is Do's.
func DoFileWithInputs(ctx context.Context, d *Declaration, name string, params map[string]string, credential string, inputs map[string][]byte, dest string, maxBytes int64, readSecret func(path string) (secret []byte, resolved string, err error)) (*Result, error) {
	return doFile(ctx, d, name, params, credential, nil, inputs, dest, maxBytes, readSecret)
}

// DoFileBody is DoFile with the request's body already built, rather than
// filled from the call's template, as DoKeyedBody is to DoKeyed: the body
// a build command printed is sent unchanged on the first request, and nil
// sends no body. readSecret is Do's.
func DoFileBody(ctx context.Context, d *Declaration, name string, params map[string]string, credential string, body json.RawMessage, dest string, maxBytes int64, readSecret func(path string) (secret []byte, resolved string, err error)) (*Result, error) {
	return doFile(ctx, d, name, params, credential, body, nil, dest, maxBytes, readSecret)
}

func doFile(ctx context.Context, d *Declaration, name string, params map[string]string, credential string, built json.RawMessage, inputs map[string][]byte, dest string, maxBytes int64, readSecret func(path string) (secret []byte, resolved string, err error)) (*Result, error) {
	address := d.Credential != nil && d.Credential.URL
	// hide is the address credential's redactor, as in doKeyed: nothing
	// said of such a call repeats the address or its host.
	var hide *regexp.Regexp
	fail := func(reason string, status int, format string, args ...any) error {
		msg := fmt.Sprintf(format, args...)
		if hide != nil {
			msg = hide.ReplaceAllLiteralString(msg, redacted)
		}
		return &CallError{Source: d.Name, Call: name, Reason: reason, Status: status, msg: msg}
	}
	c, ok := d.Call(name)
	if !ok {
		return nil, fail(NoCall, 0, "the source has no call of that name")
	}
	if len(c.Run) > 0 {
		return nil, fail(RunFailed, 0, "the call names run, and a command answers it: this makes requests alone")
	}
	if !c.Answer.File {
		return nil, fail(BadParam, 0, "the call names no file answer: its answer is JSON, which Do makes")
	}
	if c.Kind != Read {
		return nil, fail(BadParam, 0, "a file call is a read, and this call is an %s", c.Kind)
	}
	if dest == "" {
		return nil, fail(BadParam, 0, "a file call writes its answer to a file, and none was named")
	}
	if maxBytes < 0 {
		return nil, fail(BadParam, 0, "a file's bound is no bound below nothing")
	}
	if d.Credential != nil {
		switch {
		case credential == "":
			return nil, fail(NoCredential, 0, "no credential: the source's env file does not set %s", d.Credential.EnvOf(c.Kind))
		case strings.ContainsFunc(credential, func(r rune) bool { return r < ' ' || r == 0x7f }):
			return nil, fail(NoCredential, 0, "the value of %s has a control character in it, a line break perhaps: it is one line", d.Credential.EnvOf(c.Kind))
		}
	}
	var base *url.URL
	where := ""
	if address {
		u, fault := checkAddress(credential)
		if fault != addressOK {
			return nil, fail(Refused, 0, "the value of %s %s", d.Credential.EnvOf(c.Kind), fault.words())
		}
		base, where = u, "the credential's URL"
		hide = hiding(credential, u)
	} else {
		var err error
		base, err = url.Parse(d.Base)
		switch {
		case err != nil || base.Host == "" || base.User != nil:
			return nil, fail(Refused, 0, "its base is no address")
		case base.Scheme == "http" && !LoopbackHost(base.Hostname()):
			return nil, fail(Refused, 0, "its base is plain http to %s, which is not this machine: a call goes over https", base.Hostname())
		case base.Scheme != "https" && base.Scheme != "http":
			return nil, fail(Refused, 0, "its base is %s://, not https://", base.Scheme)
		}
		where = base.Host
	}
	vals, err := c.values(params, inputs, readSecret)
	if err != nil {
		return nil, fail(BadParam, 0, "%v", err)
	}
	inPath := d.Credential != nil && d.Credential.InPath
	var target string
	if address {
		target = c.addressTarget(credential, vals)
	} else if target, err = c.target(d.Base, base, vals, credential, inPath); err != nil {
		return nil, fail(BadParam, 0, "%v", err)
	}
	current, err := url.Parse(target)
	if err != nil || current.Host == "" {
		return nil, fail(Refused, 0, "its address is no address")
	}

	// The body of the first request, as doKeyed builds it: whether it has
	// one, and what method it used, decide if any redirect from it may be
	// followed at all, since a built, templated or multipart body, with a
	// secret-file's secret or a file's bytes in it, reaches that host only.
	var body io.Reader
	contentType := ""
	if built != nil {
		body = bytes.NewReader(built)
		contentType = "application/json"
	} else if c.Request.Body != nil {
		var raw []byte
		var err error
		if c.hasFileParam() {
			raw, contentType, err = c.multipart(vals, credential)
			if err != nil {
				return nil, fail(BadParam, 0, "the body cannot be sent as an upload")
			}
		} else {
			var filled any
			filled, _ = fill(c.Request.Body, vals, credential)
			raw, err = json.Marshal(filled)
			if err != nil {
				return nil, fail(BadParam, 0, "the body cannot be sent as JSON")
			}
			contentType = "application/json"
		}
		body = bytes.NewReader(raw)
	}
	bodylessGET := c.Request.Method == "GET" && body == nil
	credentialInURL := address || inPath

	start := time.Now()
	phase := newFilePhase(ctx, Timeout)
	req, err := http.NewRequestWithContext(phase.ctx, c.Request.Method, target, body)
	if err != nil {
		phase.stop()
		return nil, fail(BadParam, 0, "the request cannot be made")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "yardr")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if d.Credential != nil && d.Credential.Header != "" {
		k, v, _ := strings.Cut(d.Credential.Header, ": ")
		req.Header.Set(k, strings.Replace(v, CredentialMark, credential, 1))
	}
	cl := firstFileTransport(base.Scheme == "http")
	here := where // the host the current request goes to, in words
	redirects := 0
	for {
		// RoundTrip, not a client's Do: a client refuses a response whose
		// Location it cannot parse before its answer can be classed here
		// as a redirect with no usable location.
		resp, err := cl.RoundTrip(req)
		if err != nil {
			reason, why := phase.failure(err, start, Timeout)
			phase.stop()
			return nil, fail(reason, 0, "no answer from %s: %s", here, why)
		}
		res := &Result{Status: resp.StatusCode}
		switch {
		case res.OK():
			// The request has answered within Timeout. Only now does its
			// body get DownloadTimeout, which is long enough for the bytes
			// after a successful response without letting a slow request
			// take that long to begin.
			if !phase.extend(DownloadTimeout) {
				resp.Body.Close()
				reason, why := phase.failure(phase.ctx.Err(), start, Timeout)
				phase.stop()
				return nil, fail(reason, resp.StatusCode, "the file from %s did not begin within %s: %s", here, Timeout, why)
			}
			f, err := os.CreateTemp(filepath.Dir(dest), ".yardr-download-*")
			if err != nil {
				resp.Body.Close()
				phase.stop()
				return nil, fail(BadParam, resp.StatusCode, "the answer's file cannot be written")
			}
			temp := f.Name()
			var src io.Reader = resp.Body
			if maxBytes > 0 {
				src = io.LimitReader(resp.Body, maxBytes+1)
			}
			written, copyErr := io.Copy(f, src)
			closeErr := f.Close()
			resp.Body.Close()
			timedOut := phase.timedOut()
			reason, why := "", ""
			if copyErr != nil && !timedOut {
				reason, why = phase.failure(copyErr, start, DownloadTimeout)
			}
			phase.stop()
			switch {
			case copyErr != nil:
				os.Remove(temp)
				if timedOut {
					return nil, fail(TimedOut, resp.StatusCode, "the file from %s did not arrive within %s", here, DownloadTimeout)
				}
				return nil, fail(reason, resp.StatusCode, "the answer of %s broke off: %s", here, why)
			case closeErr != nil:
				os.Remove(temp)
				return nil, fail(BadParam, resp.StatusCode, "the answer's file cannot be written")
			case maxBytes > 0 && written > maxBytes:
				os.Remove(temp)
				return nil, fail(TooLarge, resp.StatusCode, "the file is over %d bytes, the most the caller allows for one: it is refused, not cut, and no file is left", maxBytes)
			}
			if err := os.Rename(temp, dest); err != nil {
				os.Remove(temp)
				return nil, fail(BadParam, resp.StatusCode, "the answer's file cannot be written")
			}
			res.Bytes, res.Took = int(written), time.Since(start)
			return res, nil
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			status := resp.StatusCode
			rawLoc := resp.Header.Get("Location")
			resp.Body.Close()
			phase.stop()
			failRedirect := func(format string, args ...any) (*Result, error) {
				return nil, fail(Redirect, status, format, args...)
			}
			switch {
			case !intermediateStatus(status):
				// 304 is a cache answer with no redirect in it, and 305
				// names a proxy to go through: neither is a file's address.
				return failRedirect("%s answered %d, a redirect: yardr follows none for a file but the redirects a service answers a bodyless GET with", here, status)
			case !bodylessGET:
				// No second request is made, so no byte of a built,
				// templated or multipart body, and no second method,
				// reaches the host the service chose.
				return failRedirect("%s answered %d, a redirect: yardr follows no redirect from a request that is not a bodyless GET, since its body would go where the redirect points", here, status)
			case redirects >= 5:
				return failRedirect("%s answered %d, a redirect: yardr follows at most five for a file", here, status)
			}
			loc, perr := url.Parse(rawLoc)
			if rawLoc == "" || perr != nil {
				return failRedirect("%s answered %d, a redirect with no usable location: yardr follows none without one", here, status)
			}
			// A relative reference is resolved against the first request's
			// path. Where that path is itself the credential, resolving it
			// would send the credential again; a URL with its own authority
			// or a path from the host's root names a new target without
			// inheriting it.
			if credentialInURL && loc.Scheme == "" && loc.Host == "" && !strings.HasPrefix(loc.Path, "/") {
				return failRedirect("%s answered %d, a redirect whose relative location would carry the first request's credential: yardr follows none", here, status)
			}
			hop := current.ResolveReference(loc)
			if hop.Host == "" {
				return failRedirect("%s answered %d, a redirect with no usable location: yardr follows none without one", here, status)
			}
			standin, why := checkHop(ctx, current, hop, base.Scheme == "http")
			to := hop.Host
			if address {
				to = "a redirected host"
			}
			if why != "" {
				return failRedirect("%s answered %d, a redirect to %s: %s", here, status, to, why)
			}
			redirects++
			credentialInURL = false
			phase = newFilePhase(ctx, Timeout)
			req, err = http.NewRequestWithContext(phase.ctx, http.MethodGet, hop.String(), nil)
			if err != nil {
				phase.stop()
				return failRedirect("%s answered %d, a redirect to %s: the redirected request cannot be made", here, status, to)
			}
			// A fresh bodyless GET: no credential, no body and no entity
			// header of the first request goes with it, to any host.
			cl = hopFileTransport(standin)
			current = hop
			if !address {
				here = hop.Host
			}
		default:
			// A terminal refusal, kept as a JSON call's is: with no errors
			// named it is returned unread, since it may repeat the request.
			if len(c.Answer.Errors) == 0 {
				res.Took = time.Since(start)
				resp.Body.Close()
				phase.stop()
				return res, nil
			}
			raw, err := io.ReadAll(io.LimitReader(resp.Body, AnswerLimit+1))
			resp.Body.Close()
			reason, why := "", ""
			if err != nil {
				reason, why = phase.failure(err, start, Timeout)
			}
			phase.stop()
			res.Bytes, res.Took = len(raw), time.Since(start)
			if err != nil {
				return nil, fail(reason, resp.StatusCode, "the answer of %s broke off: %s", here, why)
			} else if len(raw) > AnswerLimit {
				return nil, fail(TooLarge, resp.StatusCode, "the answer is over %d bytes, the most yardr reads of one: it is refused, not cut, since half a JSON document misleads", AnswerLimit)
			}
			kept := errorsKept(raw, c.Answer.Errors)
			if len(kept) > c.Answer.Max {
				return nil, fail(TooLarge, resp.StatusCode, "the answer is %d bytes, over the call's bound of %d (answer.max in the declaration raises it, to %d at most): it is refused, not cut, since half a JSON document misleads",
					len(kept), c.Answer.Max, AnswerLimit)
			}
			res.Answer = kept
			return res, nil
		}
	}
}

// intermediate reports whether a 3xx may be an intermediate answer for a
// file: every one but 304, which is a cache answer with no redirect in it,
// and 305, which names a proxy rather than a file's address.
func intermediateStatus(status int) bool {
	return status >= 300 && status < 400 && status != 304 && status != 305
}

// checkHop holds a redirected hop to the hop rules beside the address
// check: HTTPS or plain HTTP to this machine alone, never down from HTTPS
// and never to a name with a user in it, and dialled only to a public
// address (or, where the call's own base is plain HTTP to this machine, to
// the loopback the stand-in stands on). why says why it may not be
// followed, in host-only words; standin says it is that loopback hop.
func checkHop(ctx context.Context, from, hop *url.URL, basePlain bool) (standin bool, why string) {
	switch {
	case hop.User != nil:
		return false, "it has a user or password in it, and a redirected request goes to a host alone"
	case hop.Scheme != "https" && hop.Scheme != "http":
		return false, "it is not https://, and a file is fetched over https or plain http to this machine alone"
	case from.Scheme == "https" && hop.Scheme == "http":
		return false, "it would go down from https to plain http"
	}
	host := hop.Hostname()
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	if basePlain && hop.Scheme == "http" {
		ips, err := resolvedIPs(ctx, host)
		if err != nil || len(ips) == 0 {
			return false, "its host cannot be resolved, and no hop is dialled to a host that cannot be checked"
		}
		for _, ip := range ips {
			if !ip.IsLoopback() {
				return false, "it is plain http to a machine that is not this one, and plain http is for a stand-in on this machine alone"
			}
		}
		return true, ""
	}
	if hop.Scheme == "http" {
		return false, "it is plain http to a machine that is not this one, and a redirected file goes over https"
	}
	ips, err := resolvedIPs(ctx, host)
	if err != nil || len(ips) == 0 {
		return false, "its host cannot be resolved, and no hop is dialled to a host that cannot be checked"
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			// A name that resolves to a mixture refuses whole: no
			// address of it is picked from the rest.
			return false, "it is not a public address, and a redirected request is dialled only to one checked where the connection is made"
		}
	}
	return false, ""
}

// resolvedIPs is the addresses host names for a hop check: the address
// itself where it is one, and what lookupIPs resolves it to otherwise.
func resolvedIPs(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	if strings.EqualFold(host, "localhost") {
		return []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, nil
	}
	rctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	return lookupIPs(rctx, host)
}

// isPublicIP reports whether ip is a public, globally reachable unicast
// address: no loopback, private, link-local, multicast or unspecified
// address is one.
func isPublicIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsUnspecified()
}

// publicOnly refuses a connection to anything but a public address, in
// the shape of loopbackOnly: it is handed each address the dial actually
// connects to, so a name that rebinds between the check before the hop
// and the dial fails at the connection it makes, and a later connection
// to the same name is checked again on that connection.
func publicOnly(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if i := strings.IndexByte(host, '%'); i >= 0 { // a zone
		host = host[:i]
	}
	if ip := net.ParseIP(host); ip == nil || !isPublicIP(ip) {
		return fmt.Errorf("a redirected file goes only to a public address, and %s is not", host)
	}
	return nil
}

// firstFileTransport is the transport of a file call's first request,
// made as client makes Do's: through the process's proxy over HTTPS, held
// to the loopback over plain HTTP by loopbackOnly. No transport here
// bounds the whole answer at Timeout, which would cut a download: the
// dial and the wait for an answer's first byte keep Timeout, and
// DownloadTimeout bounds the call through its context.
func firstFileTransport(plain bool) *http.Transport {
	if plain {
		return dialCheckedTransport(loopbackOnly)
	}
	return &http.Transport{
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: Timeout,
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: Timeout}).DialContext,
	}
}

// hopFileTransport is the transport of one redirected hop: dialled
// directly with no proxy, so the address dialled is the one the hop check
// saw and the proxy never sees the address a service chose, and held at
// the connection to that check by the dial's control hook (publicOnly, or
// loopbackOnly for a stand-in's plain-HTTP hop). Its certificate is
// verified for the hop's host name as the transport makes the connection.
func hopFileTransport(standin bool) *http.Transport {
	if standin {
		return dialCheckedTransport(loopbackOnly)
	}
	return dialCheckedTransport(publicOnly)
}

// dialCheckedTransport is a direct transport held at each connection by
// a dial control hook, as client holds Do's plain-HTTP connections.
func dialCheckedTransport(control func(network, address string, c syscall.RawConn) error) *http.Transport {
	dialer := &net.Dialer{Timeout: Timeout, Control: control}
	return &http.Transport{
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: Timeout,
		DialContext:           dialer.DialContext,
	}
}

// filePhase gives the request and the successful file body different
// bounds on the same request context. A context deadline cannot be moved
// after RoundTrip returns, so a timer bounds the request first and is
// extended only once its successful response has arrived.
type filePhase struct {
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	timer   *time.Timer
	expired bool
}

func newFilePhase(parent context.Context, bound time.Duration) *filePhase {
	ctx, cancel := context.WithCancel(parent)
	p := &filePhase{ctx: ctx, cancel: cancel}
	p.timer = time.AfterFunc(bound, p.expire)
	return p
}

func (p *filePhase) expire() {
	p.mu.Lock()
	p.expired = true
	p.cancel()
	p.mu.Unlock()
}

// extend starts the download bound after the response headers arrived. It
// fails where the request timer or the caller's context already ended.
func (p *filePhase) extend(bound time.Duration) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil {
		return false
	}
	if !p.timer.Stop() {
		p.expired = true
		p.cancel()
		return false
	}
	p.timer = time.AfterFunc(bound, p.expire)
	return true
}

func (p *filePhase) stop() {
	p.mu.Lock()
	if p.timer != nil {
		p.timer.Stop()
	}
	p.cancel()
	p.mu.Unlock()
}

func (p *filePhase) timedOut() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.expired
}

func (p *filePhase) failure(err error, start time.Time, bound time.Duration) (string, string) {
	if p.timedOut() {
		return TimedOut, "nothing within " + bound.String()
	}
	return unsent(p.ctx, err), cause(p.ctx, err, start)
}
