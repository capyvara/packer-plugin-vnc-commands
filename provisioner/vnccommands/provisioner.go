package vnccommands

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/packer-plugin-sdk/bootcommand"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
	vnc "github.com/mitchellh/go-vnc"
)

type Provisioner struct {
	config Config
	interp interpolate.Context
}

var _ packer.Provisioner = (*Provisioner)(nil)

func (p *Provisioner) ConfigSpec() hcldec.ObjectSpec {
	return p.config.FlatMapstructure().HCL2Spec()
}

func (p *Provisioner) Provision(ctx context.Context, ui packer.Ui, _ packer.Communicator, data map[string]interface{}) error {
	c, err := p.runtimeConfig(data)
	if err != nil {
		return err
	}
	address := net.JoinHostPort(c.Host, strconv.Itoa(*c.Port))
	ui.Say("Connecting to VNC at " + address)
	connectCtx, cancelConnect := context.WithTimeout(ctx, *c.ConnectTimeout)
	defer cancelConnect()
	raw, err := (&net.Dialer{}).DialContext(connectCtx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connect to VNC at %s: %w", address, err)
	}
	conn := &observedConn{Conn: raw, closed: make(chan struct{})}
	defer conn.Close()
	stopConnect := context.AfterFunc(connectCtx, func() { conn.Close() })
	defer stopConnect()
	deadline, _ := connectCtx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set VNC handshake deadline: %w", err)
	}
	auth := []vnc.ClientAuth{new(vnc.ClientAuthNone)}
	if c.Password != "" {
		auth = []vnc.ClientAuth{&vnc.PasswordAuth{Password: c.Password}}
	}
	client, err := vnc.Client(conn, &vnc.ClientConfig{Auth: auth, Exclusive: false})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		message := err.Error()
		if c.Password != "" {
			message = strings.ReplaceAll(message, c.Password, "[redacted]")
		}
		return fmt.Errorf("VNC handshake/authentication at %s failed (requires RFB 3.8 and matching authentication): %s", address, message)
	}
	if !stopConnect() || connectCtx.Err() != nil {
		return fmt.Errorf("VNC handshake exceeded connect_timeout: %w", connectCtx.Err())
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("clear VNC handshake deadline: %w", err)
	}
	execCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stopExecution := context.AfterFunc(execCtx, func() { conn.Close() })
	defer stopExecution()
	// go-vnc closes its transport on EOF or an unsupported server message.
	// Observe that close so a disconnect also interrupts SDK timed waits.
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-conn.closed:
			cancel(errors.New("VNC server disconnected or closed the connection"))
		case <-watchDone:
		}
	}()
	seq, err := bootcommand.GenerateExpressionSequence(strings.Join(c.Commands, ""))
	if err != nil {
		return errors.New("could not parse runtime VNC commands")
	}
	ui.Say("Typing VNC commands")
	// The SDK supplies key mapping and modifiers. Its built-in sleep is not
	// cancellable, so impose the real interval in our KeyEvent adapter.
	driver := bootcommand.NewVNCDriver(&pacedKeys{ctx: execCtx, client: client, interval: *c.KeyInterval}, time.Nanosecond)
	if err := seq.Do(execCtx, driver); err != nil {
		if cause := context.Cause(execCtx); cause != nil {
			return fmt.Errorf("VNC command execution stopped: %w", cause)
		}
		return fmt.Errorf("type VNC commands: %w", err)
	}
	if cause := context.Cause(execCtx); cause != nil {
		return fmt.Errorf("VNC command execution stopped: %w", cause)
	}
	ui.Say("Finished typing VNC commands")
	return nil
}

type observedConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (c *observedConn) Close() error {
	var err error
	c.once.Do(func() {
		err = c.Conn.Close()
		close(c.closed)
	})
	return err
}

type pacedKeys struct {
	ctx      context.Context
	client   *vnc.ClientConn
	interval time.Duration
}

func (k *pacedKeys) KeyEvent(key uint32, down bool) error {
	if err := k.ctx.Err(); err != nil {
		return err
	}
	if err := k.client.KeyEvent(key, down); err != nil {
		return err
	}
	timer := time.NewTimer(k.interval)
	defer timer.Stop()
	select {
	case <-k.ctx.Done():
		return k.ctx.Err()
	case <-timer.C:
		return nil
	}
}
