package config

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"slices"
	"strings"

	"github.com/caarlos0/env/v11"
)

// prefix marks the service's own variables. Any of them not in Config
// fails Load, so a misspelt name can't fall back to a default.
const prefix = "MINGEST_"

// Listen is the public listen address. Config and ListenAddress both read
// it, so the healthcheck asks the port the service listens on.
type Listen struct {
	ListenAddress string `env:"MINGEST_LISTEN_ADDRESS" envDefault:":8080"`
}

// Config is everything the process learns at startup.
type Config struct {
	Listen

	DatasetSource      string     `env:"MINGEST_DATASET_SOURCE,required,notEmpty"`
	DataDir            string     `env:"MINGEST_DATA_DIR,required,notEmpty"`
	AdminListenAddress string     `env:"MINGEST_ADMIN_LISTEN_ADDRESS"             envDefault:":8081"`
	LogLevel           slog.Level `env:"MINGEST_LOG_LEVEL"                        envDefault:"info"`
	SSEMaxStreams      int        `env:"MINGEST_SSE_MAX_STREAMS"                  envDefault:"100"`

	// OTLPEndpoint turns on trace export. The OTel SDK reads the other
	// OTEL_ variables from the process environment itself.
	OTLPEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`

	// Pipeline holds constants, not set by a variable.
	Pipeline Pipeline
}

// Load parses environ, in os.Environ's form. It has no side effects.
func Load(environ []string) (Config, error) {
	vars := env.ToMap(environ)

	var cfg Config

	err := env.ParseWithOptions(&cfg, env.Options{Environment: vars})
	if err != nil {
		return Config{}, err
	}

	err = validate(cfg, vars)
	if err != nil {
		return Config{}, err
	}

	cfg.Pipeline = pipeline()

	return cfg, nil
}

// validate checks both addresses and that the stream cap is positive, and rejects any
// MINGEST_ variable that Config doesn't have. The known names come from
// Config's tags, so each name is written once.
func validate(cfg Config, vars map[string]string) error {
	errs := []error{
		checkAddress("MINGEST_LISTEN_ADDRESS", cfg.ListenAddress),
		checkAddress("MINGEST_ADMIN_LISTEN_ADDRESS", cfg.AdminListenAddress),
	}

	if cfg.SSEMaxStreams < 1 {
		errs = append(errs, fmt.Errorf(
			"MINGEST_SSE_MAX_STREAMS: %d isn't positive", cfg.SSEMaxStreams))
	}

	params, _ := env.GetFieldParams(&Config{})

	for _, name := range slices.Sorted(maps.Keys(vars)) {
		known := slices.ContainsFunc(params, func(p env.FieldParams) bool {
			return p.Key == name
		})
		if strings.HasPrefix(name, prefix) && !known {
			errs = append(errs, fmt.Errorf("unknown variable %s", name))
		}
	}

	return errors.Join(errs...)
}

func checkAddress(name, value string) error {
	_, _, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	return nil
}

// ListenAddress reads only MINGEST_LISTEN_ADDRESS, so the healthcheck
// works without the service's other variables.
func ListenAddress(environ []string) (string, error) {
	var l Listen

	err := env.ParseWithOptions(
		&l,
		env.Options{Environment: env.ToMap(environ)},
	)

	return l.ListenAddress, err
}
