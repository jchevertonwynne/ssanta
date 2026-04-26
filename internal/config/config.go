// Package config loads application configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var (
	errDstMustBePointer = errors.New("config: dst must be a non-nil pointer")
	errDstMustBeStruct  = errors.New("config: dst must point to a struct")
	errInvalidEnvTag    = errors.New("config: invalid env tag")
	errRequired         = errors.New("is required")
	errCannotSet        = errors.New("cannot set")
	errUnsupportedType  = errors.New("unsupported type")
	errSecretTooShort   = errors.New("SESSION_SECRET must be at least 32 bytes")
)

// minSessionSecretBytes is the hard floor for HMAC key length. 32 bytes
// matches SHA-256 block/output sizing and leaves no room for weak dev secrets
// to accidentally reach non-local environments.
const minSessionSecretBytes = 32

// Config contains all runtime configuration values for the application.
type Config struct {
	HTTPAddr           string `env:"HTTP_ADDR,:8080"`
	DatabaseURL        string `env:"DATABASE_URL"`
	DatabaseSchema     string `env:"DATABASE_SCHEMA,"`
	MigrateDatabaseURL string `env:"MIGRATE_DATABASE_URL,"`
	MigrationsDir      string `env:"MIGRATIONS_DIR,migrations"`
	SessionSecret      string `env:"SESSION_SECRET"`

	SecureCookies     bool   `env:"SECURE_COOKIES,true"`
	TrustProxyHeaders bool   `env:"TRUST_PROXY_HEADERS,false"`
	MetricsSecret     string `env:"METRICS_SECRET,"`

	RateLimitAuthMax      int           `env:"RATE_LIMIT_AUTH_MAX,5"`
	RateLimitAuthWindow   time.Duration `env:"RATE_LIMIT_AUTH_WINDOW,1m"`
	RateLimitSearchMax    int           `env:"RATE_LIMIT_SEARCH_MAX,30"`
	RateLimitSearchWindow time.Duration `env:"RATE_LIMIT_SEARCH_WINDOW,1m"`
	RateLimitRoomMax      int           `env:"RATE_LIMIT_ROOM_MAX,10"`
	RateLimitRoomWindow   time.Duration `env:"RATE_LIMIT_ROOM_WINDOW,1m"`
	RateLimitInviteMax    int           `env:"RATE_LIMIT_INVITE_MAX,20"`
	RateLimitInviteWindow time.Duration `env:"RATE_LIMIT_INVITE_WINDOW,1m"`
	RateLimitWSMax        int           `env:"RATE_LIMIT_WS_MAX,10"`
	RateLimitWSWindow     time.Duration `env:"RATE_LIMIT_WS_WINDOW,1m"`
	RateLimitDMMax        int           `env:"RATE_LIMIT_DM_MAX,10"`
	RateLimitDMWindow     time.Duration `env:"RATE_LIMIT_DM_WINDOW,1m"`

	WSMessageBurst        int     `env:"WS_MSG_BURST,10"`
	WSMessageRefillPerSec float64 `env:"WS_MSG_REFILL_PER_SEC,5"`

	InviteMaxAge        time.Duration `env:"INVITE_MAX_AGE,24h"`
	JanitorInterval     time.Duration `env:"JANITOR_INTERVAL,5m"`
	SessionTTL          time.Duration `env:"SESSION_TTL,168h"`
	RoomPGPChallengeTTL time.Duration `env:"ROOM_PGP_CHALLENGE_TTL,10m"`

	Argon2Memory      uint32 `env:"ARGON2_MEMORY,65536"`
	Argon2Iterations  uint32 `env:"ARGON2_ITERATIONS,1"`
	Argon2Parallelism uint8  `env:"ARGON2_PARALLELISM,4"`

	ServiceName string `env:"OTEL_SERVICE_NAME,ssanta"`
	Environment string `env:"DEPLOYMENT_ENVIRONMENT,local"`
}

// Load reads configuration from environment variables.
func Load() (Config, error) {
	var cfg Config
	if err := loadFromEnv(&cfg); err != nil {
		return Config{}, err
	}
	if len(cfg.SessionSecret) < minSessionSecretBytes {
		return Config{}, errSecretTooShort
	}
	return cfg, nil
}

//nolint:cyclop
func loadFromEnv(dst any) error {
	value := reflect.ValueOf(dst)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return errDstMustBePointer
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return errDstMustBeStruct
	}
	structType := value.Type()

	for i := range structType.NumField() {
		field := structType.Field(i)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("env")
		if tag == "" {
			continue
		}

		name, def, hasDefault := parseEnvTag(tag)
		if name == "" {
			return fmt.Errorf("%w on %s", errInvalidEnvTag, field.Name)
		}

		raw, ok := os.LookupEnv(name)
		if !ok {
			if !hasDefault {
				return fmt.Errorf("%s %w", name, errRequired)
			}
			raw = def
		}

		raw = strings.TrimSpace(raw)
		if raw == "" {
			if !hasDefault {
				return fmt.Errorf("%s %w", name, errRequired)
			}
			raw = strings.TrimSpace(def)
			if raw == "" {
				continue
			}
		}

		if err := setFromString(value.Field(i), raw); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	return nil
}

func parseEnvTag(tag string) (string, string, bool) {
	parts := strings.SplitN(tag, ",", 2)
	name := strings.TrimSpace(parts[0])
	def := ""
	hasDefault := false
	if len(parts) == 2 {
		def = parts[1]
		hasDefault = true
	}
	return name, def, hasDefault
}

//nolint:cyclop
func setFromString(v reflect.Value, raw string) error {
	if !v.CanSet() {
		return errCannotSet
	}
	if v.Type() == reflect.TypeFor[time.Duration]() {
		d, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		v.SetInt(int64(d))
		return nil
	}

	switch v.Kind() { //nolint:exhaustive // we handle all relevant types
	case reflect.String:
		v.SetString(raw)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetInt(n)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(strings.TrimSpace(raw), 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetUint(n)
		return nil
	case reflect.Bool:
		b, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		v.SetBool(b)
		return nil
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetFloat(f)
		return nil
	default:
		return fmt.Errorf("%w %s", errUnsupportedType, v.Type())
	}
}
