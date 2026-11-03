package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"mingest/pkg/config"
)

// healthcheckTimeout is how long the check waits for /status. Docker's
// HEALTHCHECK gives it 3 s.
const healthcheckTimeout = 2 * time.Second

// errPortZero is a listen address whose port is picked at startup, which
// the check can't know.
var errPortZero = errors.New(
	"the listen address has port 0, which can't be checked",
)

// healthcheck asks the service's /status as JSON, for Docker's
// HEALTHCHECK, and fails unless the answer is 200 within
// healthcheckTimeout. It needs only MINGEST_LISTEN_ADDRESS.
func healthcheck(ctx context.Context, environ []string) error {
	url, err := statusURL(environ)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s answered %s", url, resp.Status)
	}

	return nil
}

// statusURL is /status on the listen address. An empty or unspecified host
// (0.0.0.0, ::) is checked on 127.0.0.1.
func statusURL(environ []string) (string, error) {
	addr, err := config.ListenAddress(environ)
	if err != nil {
		return "", err
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}

	if port == "0" {
		return "", errPortZero
	}

	if host == "" || net.ParseIP(host).IsUnspecified() {
		host = "127.0.0.1"
	}

	return "http://" + net.JoinHostPort(host, port) + "/status", nil
}
