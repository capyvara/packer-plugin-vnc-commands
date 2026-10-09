package vnccommands

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/bootcommand"
	"github.com/hashicorp/packer-plugin-sdk/common"
	"github.com/hashicorp/packer-plugin-sdk/template/config"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
)

//go:generate go run github.com/hashicorp/packer-plugin-sdk/cmd/packer-sdc@v0.6.12 mapstructure-to-hcl2 -type Config

type Config struct {
	common.PackerConfig `mapstructure:",squash"`
	Host                string         `mapstructure:"host" required:"true"`
	Port                *int           `mapstructure:"port"`
	Password            string         `mapstructure:"password" required:"false"`
	Commands            []string       `mapstructure:"commands" required:"true"`
	KeyInterval         *time.Duration `mapstructure:"key_interval" required:"false"`
	ConnectTimeout      *time.Duration `mapstructure:"connect_timeout" required:"false"`
}

func (p *Provisioner) Prepare(raws ...interface{}) error {
	p.config = Config{}
	p.interp = interpolate.Context{}
	// Defer these strings to Provision so JSON build values can be resolved.
	if err := config.Decode(&p.config, &config.DecodeOpts{
		Interpolate: true, InterpolateContext: &p.interp,
		InterpolateFilter: &interpolate.RenderFilter{Exclude: []string{"host", "password", "commands"}},
	}, raws...); err != nil {
		return fmt.Errorf("decode VNC configuration: %w", err)
	}
	if p.config.KeyInterval == nil {
		interval := 100 * time.Millisecond
		p.config.KeyInterval = &interval
	}
	if p.config.ConnectTimeout == nil {
		timeout := 30 * time.Second
		p.config.ConnectTimeout = &timeout
	}
	if p.config.Port == nil {
		port := 5900
		p.config.Port = &port
	}
	return p.config.validate()
}

func (c *Config) validate() error {
	var errs []error
	if strings.TrimSpace(c.Host) == "" {
		errs = append(errs, errors.New("host must be a nonempty IP address or DNS name"))
	}
	if c.Port == nil || *c.Port < 1 || *c.Port > 65535 {
		errs = append(errs, errors.New("port must be between 1 and 65535"))
	}
	if c.KeyInterval == nil || *c.KeyInterval <= 0 {
		errs = append(errs, errors.New("key_interval must be a positive duration"))
	}
	if c.ConnectTimeout == nil || *c.ConnectTimeout <= 0 {
		errs = append(errs, errors.New("connect_timeout must be a positive duration"))
	}
	if len(c.Commands) == 0 || strings.Join(c.Commands, "") == "" {
		errs = append(errs, errors.New("commands must contain at least one nonempty command"))
	} else {
		seq, err := bootcommand.GenerateExpressionSequence(strings.Join(c.Commands, ""))
		if err != nil {
			// Parser errors may contain typed text. Keep it out of normal output.
			errs = append(errs, errors.New("commands could not be parsed as Packer boot-command expressions"))
		} else if len(seq.Validate()) > 0 {
			errs = append(errs, errors.New("commands contain an invalid Packer expression; waits must be positive"))
		}
	}
	return errors.Join(errs...)
}

func (p *Provisioner) runtimeConfig(data map[string]interface{}) (Config, error) {
	c := p.config
	interp := p.interp
	interp.Data = data
	var err error
	for _, field := range []struct {
		name  string
		value *string
	}{{"host", &c.Host}, {"password", &c.Password}} {
		*field.value, err = interpolate.Render(*field.value, &interp)
		if err != nil {
			return c, fmt.Errorf("could not interpolate VNC %s", field.name)
		}
	}
	c.Commands = make([]string, len(p.config.Commands))
	for i, command := range p.config.Commands {
		c.Commands[i], err = interpolate.Render(command, &interp)
		if err != nil {
			return c, fmt.Errorf("could not interpolate VNC commands[%d]", i)
		}
	}
	return c, c.validate()
}
