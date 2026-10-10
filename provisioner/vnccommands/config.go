package vnccommands

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/common"
	"github.com/hashicorp/packer-plugin-sdk/template/config"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
)

//go:generate go run github.com/hashicorp/packer-plugin-sdk/cmd/packer-sdc@v0.6.12 mapstructure-to-hcl2 -type Config

type Config struct {
	common.PackerConfig `mapstructure:",squash"`
	Host                string         `mapstructure:"host" required:"true"`
	Port                *int           `mapstructure:"port"`
	Username            string         `mapstructure:"username" required:"false"`
	Password            string         `mapstructure:"password" required:"false"`
	Commands            []string       `mapstructure:"commands" required:"true"`
	KeyInterval         *time.Duration `mapstructure:"key_interval" required:"false"`
	ConnectTimeout      *time.Duration `mapstructure:"connect_timeout" required:"false"`
	Preview             bool           `mapstructure:"preview" required:"false"`
	PreviewAutoOpen     bool           `mapstructure:"preview_auto_open" required:"false"`
	RecordingPath       string         `mapstructure:"recording_path" required:"false"`
	FrameRate           *int           `mapstructure:"frame_rate" required:"false"`
}

func (p *Provisioner) Prepare(raws ...interface{}) error {
	p.config = Config{}
	p.interp = interpolate.Context{}
	// Defer these strings to Provision so JSON build values can be resolved.
	if err := config.Decode(&p.config, &config.DecodeOpts{
		Interpolate: true, InterpolateContext: &p.interp,
		InterpolateFilter: &interpolate.RenderFilter{Exclude: []string{"host", "username", "password", "commands", "recording_path"}},
	}, raws...); err != nil {
		// SDK interpolation errors append the original field value after
		// this delimiter. Preserve the diagnostic without echoing that value.
		message := strings.SplitN(err.Error(), " in:\n\n", 2)[0]
		return fmt.Errorf("decode VNC configuration: %s", message)
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
	if p.config.FrameRate == nil {
		rate := 5
		p.config.FrameRate = &rate
	}
	return p.config.validate(true)
}

func (c *Config) validate(allowTemplates bool) error {
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
	if c.FrameRate == nil || *c.FrameRate < 1 || *c.FrameRate > 60 {
		errs = append(errs, errors.New("frame_rate must be between 1 and 60"))
	}
	if c.PreviewAutoOpen && !c.Preview {
		errs = append(errs, errors.New("preview_auto_open requires preview = true"))
	}
	if len(c.Commands) == 0 || strings.Join(c.Commands, "") == "" {
		errs = append(errs, errors.New("commands must contain at least one nonempty command"))
	} else {
		if _, err := parseCommands(strings.Join(c.Commands, ""), allowTemplates); err != nil {
			errs = append(errs, err)
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
	}{{"host", &c.Host}, {"username", &c.Username}, {"password", &c.Password}, {"recording_path", &c.RecordingPath}} {
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
	// Check the actual credentials after rendering: a template expression can
	// be longer than the credential it produces.
	if c.Username != "" {
		if err := validateAppleCredentials(c.Username, c.Password); err != nil {
			return c, err
		}
	}
	if c.RecordingPath != "" && !strings.EqualFold(filepath.Ext(c.RecordingPath), ".avi") {
		return c, errors.New("recording_path must end in .avi")
	}
	return c, c.validate(false)
}
