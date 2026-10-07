// Package httpx provides the CLI's SSRF-hardened HTTP client and a
// rate-limited, retrying request helper shared by every fetch the CLI makes.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

const (
	DialTimeout = 10 * time.Second
	ReadTimeout = 60 * time.Second

	// Workers bounds concurrent download/crawl work across the CLI.
	// NewClient deliberately sizes MaxConnsPerHost below it so a burst of
	// workers queues on the connection pool rather than hammering a single
	// host, independent of how many sites are crawled at once.
	Workers = 8

	UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) " +
		"Chrome/148.0.0.0 Safari/537.36"

	maxRetries  = 4
	baseBackoff = time.Second
)

// ErrTooManyRetries indicates a request exhausted its retry budget, whether
// against repeated 429s or repeated transient network errors.
var ErrTooManyRetries = errors.New("too many retries")

// NewClient returns an http.Client whose dialer refuses to connect to
// non-public addresses (see dialPublic).
func NewClient() *http.Client {
	dialer := &net.Dialer{Timeout: DialTimeout}
	return &http.Client{
		Timeout: ReadTimeout,
		Transport: &http.Transport{
			DialContext:         dialPublic(dialer.DialContext),
			MaxIdleConns:        32,
			MaxIdleConnsPerHost: 8,
			MaxConnsPerHost:     4, // deliberately less than Workers (8), see const doc above
			IdleConnTimeout:     30 * time.Second,
		},
	}
}

// dialPublic wraps a DialContext func to reject connections that resolve to
// non-public addresses. This guards against SSRF (e.g. a scraped or
// API-supplied URL resolving to an internal service or a cloud metadata
// endpoint) if -site/-api/-input input is ever less trusted than "chosen by
// the person running this CLI". The check runs against the actual resolved
// remote address after connecting, not the hostname before resolution, so it
// also covers HTTP redirect targets and closes the DNS-rebinding TOCTOU gap
// a pre-resolve check would leave open.
func dialPublic(dial func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}

		host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("parse remote address: %w", err)
		}

		ip := net.ParseIP(host)
		if ip == nil || isDisallowedIP(ip) {
			conn.Close()
			return nil, fmt.Errorf("refusing to connect to disallowed address %s", host)
		}

		return conn, nil
	}
}

// extraBlockedRanges covers special-purpose ranges not caught by net.IP's
// built-in classifiers: RFC 6598 shared address space (CGNAT, used
// internally by many cloud NAT/load-balancer setups), IETF protocol
// assignments, and documentation/benchmarking ranges that should never be a
// legitimate scrape target.
var extraBlockedRanges = mustParseCIDRs(
	"100.64.0.0/10",   // RFC 6598 shared address space (CGNAT)
	"192.0.0.0/24",    // RFC 6890 IETF protocol assignments
	"192.0.2.0/24",    // RFC 5737 documentation (TEST-NET-1)
	"198.18.0.0/15",   // RFC 2544 benchmarking
	"198.51.100.0/24", // RFC 5737 documentation (TEST-NET-2)
	"203.0.113.0/24",  // RFC 5737 documentation (TEST-NET-3)
	"240.0.0.0/4",     // RFC 1112 reserved
	"100::/64",        // RFC 6666 discard-only
	"2001:db8::/32",   // RFC 3849 documentation
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(fmt.Sprintf("httpx: invalid CIDR %q: %v", c, err))
		}
		nets = append(nets, n)
	}
	return nets
}

func isDisallowedIP(ip net.IP) bool {
	if ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified() ||
		ip.IsMulticast() {
		return true
	}

	for _, n := range extraBlockedRanges {
		if n.Contains(ip) {
			return true
		}
	}

	return false
}

// NewRequest builds a context-bound request with the CLI's User-Agent set.
func NewRequest(ctx context.Context, method, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", UserAgent)

	return req, nil
}

// Do executes req respecting limiter's rate limit, retrying on HTTP 429
// (honoring Retry-After when present) and on transient network errors, up
// to maxRetries times with jittered exponential backoff. A canceled ctx
// aborts immediately without consuming a retry.
func Do(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, req *http.Request) (*http.Response, error) {
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}

	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		cloned := req.Clone(ctx)

		// req.Clone shares the original Body reader rather than copying it,
		// so a retry after the first attempt would send an already-drained
		// body. Every caller in this codebase issues GET requests with a nil
		// body, so this is currently unreachable, but guard it explicitly
		// rather than silently corrupting a future request that isn't.
		if req.Body != nil {
			if req.GetBody == nil {
				return nil, fmt.Errorf("retry request has a body but no GetBody: cannot safely retry")
			}

			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("rewind request body: %w", err)
			}
			cloned.Body = body
		}

		resp, err := client.Do(cloned)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}

			lastErr = err
			if attempt == maxRetries {
				break
			}

			wait := jitteredBackoff(attempt)
			logger.Warn("transient request error, retrying",
				"url", req.URL, "attempt", attempt+1, "retry_in", wait, "error", err)

			if serr := sleep(ctx, wait); serr != nil {
				return nil, serr
			}
			continue
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		resp.Body.Close()
		lastErr = fmt.Errorf("rate limited: %s", resp.Status)

		if attempt == maxRetries {
			break
		}

		wait := retryAfter(resp)
		if wait <= 0 {
			wait = jitteredBackoff(attempt)
		}

		logger.Warn("rate limited", "url", req.URL, "attempt", attempt+1, "retry_in", wait)

		if serr := sleep(ctx, wait); serr != nil {
			return nil, serr
		}
	}

	return nil, fmt.Errorf("%w after %d attempts: %w", ErrTooManyRetries, maxRetries+1, lastErr)
}

func retryAfter(resp *http.Response) time.Duration {
	retry := resp.Header.Get("Retry-After")
	if retry == "" {
		return 0
	}
	if secs, err := strconv.Atoi(retry); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(retry); err == nil {
		return time.Until(t)
	}
	return 0
}

func jitteredBackoff(attempt int) time.Duration {
	wait := baseBackoff << attempt
	return wait + time.Duration(rand.Int63n(int64(500*time.Millisecond)))
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
