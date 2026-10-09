package vnccommands

import (
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"
)

func TestPrepareDefaults(t *testing.T) {
	p := new(Provisioner)
	if err := p.Prepare(map[string]interface{}{"host": "::1", "commands": []string{"hello", "<enter>"}}); err != nil {
		t.Fatal(err)
	}
	if *p.config.Port != 5900 || p.config.Password != "" || *p.config.KeyInterval != 100*time.Millisecond || *p.config.ConnectTimeout != 30*time.Second {
		t.Fatalf("unexpected defaults: %+v", p.config)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value interface{}
		want  string
	}{
		{"empty host", "host", "", "host"},
		{"blank host", "host", "  ", "host"},
		{"empty commands", "commands", []string{}, "commands"},
		{"empty text", "commands", []string{"", ""}, "commands"},
		{"port zero", "port", 0, "port"},
		{"port negative", "port", -1, "port"},
		{"port too large", "port", 65536, "port"},
		{"interval zero", "key_interval", "0s", "key_interval"},
		{"interval negative", "key_interval", "-1s", "key_interval"},
		{"interval invalid", "key_interval", "nope", "key_interval"},
		{"timeout zero", "connect_timeout", "0s", "connect_timeout"},
		{"timeout negative", "connect_timeout", "-1s", "connect_timeout"},
		{"timeout invalid", "connect_timeout", "nope", "connect_timeout"},
		{"invalid SDK wait", "commands", []string{"sensitive-text<wait0s>"}, "positive"},
		{"unknown field", "typo", true, "unknown configuration"},
		{"template syntax", "commands", []string{"sensitive-text{{"}, "commands"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]interface{}{"host": "localhost", "commands": []string{"hi"}}
			raw[tc.key] = tc.value
			err := new(Provisioner).Prepare(raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "sensitive-text") {
				t.Fatal("validation error exposed command contents")
			}
		})
	}
}

func TestRuntimeConfiguration(t *testing.T) {
	p := new(Provisioner)
	err := p.Prepare(map[string]interface{}{
		"host": "{{.Host}}", "password": "{{user `vnc_password`}}",
		"commands":              []string{"{{user `prefix`}}", "{{.Name}}<enter>"},
		"packer_user_variables": map[string]string{"prefix": "hello ", "vnc_password": "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := p.runtimeConfig(map[string]interface{}{"Host": "::1", "Name": "guest"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "::1" || c.Password != "secret" || strings.Join(c.Commands, "") != "hello guest<enter>" {
		t.Fatalf("unexpected runtime values: %+v", c)
	}
	if p.config.Host != "{{.Host}}" || p.config.Commands[1] != "{{.Name}}<enter>" {
		t.Fatal("runtime rendering mutated the prepared configuration")
	}
}

func TestHCLDecodeAndRepeatedPrepare(t *testing.T) {
	p := new(Provisioner)
	raw := cty.ObjectVal(map[string]cty.Value{
		"host": cty.StringVal("localhost"), "port": cty.NumberIntVal(5901),
		"password": cty.NullVal(cty.String), "commands": cty.ListVal([]cty.Value{cty.StringVal("<wait2s>")}),
		"key_interval": cty.NullVal(cty.String), "connect_timeout": cty.NullVal(cty.String),
	})
	if err := p.Prepare(raw); err != nil {
		t.Fatal(err)
	}
	if *p.config.Port != 5901 || *p.config.KeyInterval != 100*time.Millisecond {
		t.Fatal("HCL defaults or overrides failed")
	}
	if err := p.Prepare(map[string]interface{}{"host": "localhost", "commands": []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if *p.config.Port != 5900 {
		t.Fatal("Prepare retained the previous configuration")
	}
}
