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

type Config struct {
	HTTPAddr           string `env:"HTTP_ADDR,:8080"`
	DatabaseURL        string `env:"DATABASE_URL"`
	DatabaseSchema     string `env:"DATABASE_SCHEMA,"`
	MigrateDatabaseURL string `env:"MIGRATE_DATABASE_URL,"`
	MigrationsDir      string `env:"MIGRATIONS_DIR,migrations"`
	SessionSecret      string `env:"SESSION_SECRET"`

	SecureCookies bool `env:"SECURE_COOKIES,true"`

	InviteMaxAge        time.Duration `env:"INVITE_MAX_AGE,24h"`
	JanitorInterval     time.Duration `env:"JANITOR_INTERVAL,1m"`
	SessionTTL          time.Duration `env:"SESSION_TTL,168h"`
	RoomPGPChallengeTTL time.Duration `env:"ROOM_PGP_CHALLENGE_TTL,10m"`

	Argon2Memory      uint32 `env:"ARGON2_MEMORY,65536"`
	Argon2Iterations  uint32 `env:"ARGON2_ITERATIONS,1"`
	Argon2Parallelism uint8  `env:"ARGON2_PARALLELISM,4"`

	OTLPEndpoint string `env:"OTLP_ENDPOINT,"`
	OTLPInsecure bool   `env:"OTLP_INSECURE,true"`
	ServiceName  string `env:"OTEL_SERVICE_NAME,ssanta"`
	Environment  string `env:"DEPLOYMENT_ENVIRONMENT,local"`
}

func Load() (Config, error) {
	var cfg Config
	if err := loadFromEnv(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadFromEnv(dst any) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("config: dst must be a non-nil pointer")
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return errors.New("config: dst must point to a struct")
	}
	rt := rv.Type()

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("env")
		if tag == "" {
			continue
		}

		name, def, hasDefault := parseEnvTag(tag)
		if name == "" {
			return fmt.Errorf("config: invalid env tag on %s", field.Name)
		}

		raw, ok := os.LookupEnv(name)
		if !ok {
			if !hasDefault {
				return fmt.Errorf("%s is required", name)
			}
			raw = def
		}

		raw = strings.TrimSpace(raw)
		if raw == "" {
			if !hasDefault {
				return fmt.Errorf("%s is required", name)
			}
			raw = strings.TrimSpace(def)
			if raw == "" {
				continue
			}
		}

		if err := setFromString(rv.Field(i), raw); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	return nil
}

func parseEnvTag(tag string) (name, def string, hasDefault bool) {
	parts := strings.SplitN(tag, ",", 2)
	name = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		def = parts[1]
		hasDefault = true
	}
	return name, def, hasDefault
}

func setFromString(v reflect.Value, raw string) error {
	if !v.CanSet() {
		return errors.New("cannot set")
	}
	if v.Type() == reflect.TypeOf(time.Duration(0)) {
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
	default:
		return fmt.Errorf("unsupported type %s", v.Type())
	}
}
