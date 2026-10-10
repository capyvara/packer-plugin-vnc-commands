package vnccommands

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/hashicorp/packer-plugin-sdk/bootcommand"
	vnc "github.com/mitchellh/go-vnc"
)

// Keep the SDK's parser and driver for keyboard expressions. All segments
// share one driver, so held keys survive mouse commands and list boundaries.
type keyboardSequence interface {
	Do(context.Context, bootcommand.BCDriver) error
}

type commandStep struct {
	keys  keyboardSequence
	click *mouseClick
}

type commandSequence struct {
	steps    []commandStep
	hasMouse bool
}

func parseCommands(command string, allowTemplates bool) (commandSequence, error) {
	var seq commandSequence
	addKeys := func(text string) error {
		if text == "" {
			return nil
		}
		keys, err := bootcommand.GenerateExpressionSequence(text)
		if err != nil {
			return errors.New("commands could not be parsed as Packer boot-command expressions")
		}
		if len(keys.Validate()) > 0 {
			return errors.New("commands contain an invalid Packer expression; waits must be positive")
		}
		seq.steps = append(seq.steps, commandStep{keys: keys})
		return nil
	}
	start, scan := 0, 0
	for scan < len(command) {
		offset := strings.IndexByte(command[scan:], '<')
		if offset < 0 {
			break
		}
		open := scan + offset
		endName := open + 1
		for endName < len(command) && ((command[endName] >= 'a' && command[endName] <= 'z') || (command[endName] >= 'A' && command[endName] <= 'Z')) {
			endName++
		}
		name := command[open+1 : endName]
		button := vnc.ButtonLeft
		switch {
		case strings.EqualFold(name, "click"):
		case strings.EqualFold(name, "rightClick"):
			button = vnc.ButtonRight
		default:
			scan = open + 1
			continue
		}
		closeOffset := strings.IndexByte(command[endName:], '>')
		if closeOffset < 0 {
			return seq, errors.New("mouse commands must end with >")
		}
		close := endName + closeOffset
		body := command[endName:close]
		if body == "" || !unicode.IsSpace([]rune(body)[0]) {
			return seq, errors.New("mouse commands require coordinates: <click x,y> or <rightClick x,y>")
		}
		click := &mouseClick{button: button}
		if allowTemplates && strings.Contains(body, "{{") {
			// Prepare runs before legacy JSON build values are available. The
			// rendered command is parsed strictly again before connecting.
			click.deferred = true
		} else {
			coordinates := strings.Split(strings.TrimSpace(body), ",")
			if len(coordinates) != 2 {
				return seq, errors.New("mouse commands require exactly two coordinates separated by a comma")
			}
			var err error
			click.x, err = parseCoordinate(coordinates[0])
			if err != nil {
				return seq, err
			}
			click.y, err = parseCoordinate(coordinates[1])
			if err != nil {
				return seq, err
			}
		}
		if err := addKeys(command[start:open]); err != nil {
			return seq, err
		}
		seq.steps = append(seq.steps, commandStep{click: click})
		seq.hasMouse = true
		start, scan = close+1, close+1
	}
	err := addKeys(command[start:])
	return seq, err
}

func (s commandSequence) Do(ctx context.Context, driver bootcommand.BCDriver, writer *vncWriter, interval time.Duration) error {
	for _, step := range s.steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if step.keys != nil {
			if err := step.keys.Do(ctx, driver); err != nil {
				return err
			}
		} else if err := step.click.Do(ctx, writer, interval); err != nil {
			return err
		}
	}
	return nil
}

type coordinate struct {
	value   float64
	percent bool
}

var pixelCoordinate = regexp.MustCompile(`^[0-9]+$`)
var percentCoordinate = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)%$`)

func parseCoordinate(text string) (coordinate, error) {
	text = strings.TrimSpace(text)
	if percentCoordinate.MatchString(text) {
		value, err := strconv.ParseFloat(strings.TrimSuffix(text, "%"), 64)
		if err == nil && value <= 100 {
			return coordinate{value: value, percent: true}, nil
		}
	} else if pixelCoordinate.MatchString(text) {
		value, err := strconv.ParseUint(text, 10, 16)
		if err == nil {
			return coordinate{value: float64(value)}, nil
		}
	}
	// Do not include commands or interpolation results in diagnostics.
	return coordinate{}, errors.New("mouse coordinates must be integer pixels from 0 to 65535 or percentages from 0% to 100%")
}

func (c coordinate) resolve(size uint16) (uint16, error) {
	if size == 0 {
		return 0, errors.New("VNC framebuffer dimensions must be nonzero")
	}
	value := c.value
	if c.percent {
		value = math.Round(value / 100 * float64(size-1))
	}
	if value >= float64(size) {
		return 0, errors.New("mouse coordinates are outside the VNC framebuffer")
	}
	return uint16(value), nil
}

type mouseClick struct {
	button   vnc.ButtonMask
	x, y     coordinate
	deferred bool
}

func (c *mouseClick) Do(ctx context.Context, writer *vncWriter, interval time.Duration) error {
	if c.deferred {
		return errors.New("mouse coordinates must be rendered before execution")
	}
	width, height := writer.size.get()
	x, err := c.x.resolve(width)
	if err != nil {
		return err
	}
	y, err := c.y.resolve(height)
	if err != nil {
		return err
	}
	// Move first so the guest can update hover/focus before the button press.
	if err := writer.PointerEvent(0, x, y); err != nil {
		return err
	}
	if err := waitInterval(ctx, interval); err != nil {
		return err
	}
	if err := writer.PointerEvent(c.button, x, y); err != nil {
		return err
	}
	err = waitInterval(ctx, interval)
	// Attempt a release even when the hold was interrupted. A disconnected
	// transport cannot guarantee delivery; never replay the click.
	releaseErr := writer.PointerEvent(0, x, y)
	if err != nil {
		return err
	}
	if releaseErr != nil {
		return releaseErr
	}
	return waitInterval(ctx, interval)
}

func waitInterval(ctx context.Context, interval time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
