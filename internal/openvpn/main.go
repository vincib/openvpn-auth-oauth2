package openvpn

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jkroepke/openvpn-auth-oauth2/internal/config"
	"github.com/jkroepke/openvpn-auth-oauth2/internal/openvpn/connection"
	"github.com/jkroepke/openvpn-auth-oauth2/internal/state"
	"github.com/jkroepke/openvpn-auth-oauth2/internal/utils"
)

func NewClient(logger *slog.Logger, conf config.Config) *Client {
	return &Client{
		conf:   conf,
		logger: logger,

		closed: false,
		mu:     sync.Mutex{},

		errCh:             make(chan error, 1),
		clientsCh:         make(chan connection.Client, 10),
		commandResponseCh: make(chan string, 10),
		commandsCh:        make(chan string, 10),
		shutdownCh:        make(chan struct{}, 1),
	}
}

func (c *Client) Connect() error {
	var err error

	c.logger.Info(utils.StringConcat("connect to openvpn management interface ", c.conf.OpenVpn.Addr.String()))

	if err = c.setupConnection(); err != nil {
		return fmt.Errorf("unable to connect to openvpn management interface %s: %w", c.conf.OpenVpn.Addr.String(), err)
	}

	defer c.conn.Close()

	c.scanner = bufio.NewScanner(c.conn)
	c.scanner.Split(bufio.ScanLines)

	if err = c.handlePassword(); err != nil {
		return err
	}

	go c.handleMessages()
	go c.handleClients()
	go c.handleCommands()

	err = c.releaseManagementHold()
	if err != nil {
		return err
	}

	c.logger.Info("connection to OpenVPN management interfaced established.")

	err = c.checkManagementInterfaceVersion()
	if err != nil {
		return err
	}

	for {
		select {
		case err := <-c.errCh:
			c.close()

			if err != nil {
				return fmt.Errorf("OpenVPN management error: %w", err)
			}

			return nil
		case <-c.shutdownCh:
			c.close()

			return nil
		}
	}
}

func (c *Client) setupConnection() error {
	var err error

	switch c.conf.OpenVpn.Addr.Scheme {
	case "tcp":
		c.conn, err = net.Dial(c.conf.OpenVpn.Addr.Scheme, c.conf.OpenVpn.Addr.Host)
	case "unix":
		c.conn, err = net.Dial(c.conf.OpenVpn.Addr.Scheme, c.conf.OpenVpn.Addr.Path)
	default:
		err = fmt.Errorf("unable to connect to openvpn management interface: %w %s", ErrUnknownProtocol, c.conf.OpenVpn.Addr.Scheme)
	}

	return err
}

func (c *Client) releaseManagementHold() error {
	resp, err := c.SendCommand("hold release")
	if err != nil {
		return fmt.Errorf("error from hold release command: %w", err)
	}

	if !strings.HasPrefix(resp, "SUCCESS:") {
		return fmt.Errorf("error from hold release command: %w: %s", ErrErrorResponse, resp)
	}

	return nil
}

func (c *Client) checkManagementInterfaceVersion() error {
	resp, err := c.SendCommand("version")
	if err != nil {
		return fmt.Errorf("error from version command: %w", err)
	}

	if !strings.HasPrefix(resp, "OpenVPN Version: ") {
		return fmt.Errorf("error from version command: %w: %s", ErrErrorResponse, resp)
	}

	versionParts := strings.Split(resp, "\n")

	if len(versionParts) != 4 {
		return fmt.Errorf("unexpected response from version command: %s", resp)
	}

	c.logger.Info(utils.StringConcat(versionParts[0], " - ", versionParts[1]))

	managementInterfaceVersion, err := strconv.Atoi(versionParts[1][len(versionParts[1])-1:])
	if err != nil {
		return fmt.Errorf("unable to parse openvpn management interface version: %w", err)
	}

	if managementInterfaceVersion < 5 {
		return errors.New("openvpn-auth-oauth2 requires OpenVPN management interface version 5 or higher")
	}

	return nil
}

func (c *Client) checkClientSsoCapabilities(logger *slog.Logger, client connection.Client) bool {
	if strings.Contains(client.IvSSO, "webauth") {
		return true
	}

	errorSsoNotSupported := "OpenVPN Client does not support SSO authentication via webauth"
	logger.Warn(errorSsoNotSupported)
	c.DenyClient(logger, state.ClientIdentifier{Cid: client.Cid, Kid: client.Kid}, errorSsoNotSupported)

	return false
}

// Shutdown shutdowns the client connection.
func (c *Client) Shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.closed {
		c.logger.Info("shutdown OpenVPN management connection")
		c.shutdownCh <- struct{}{}
	}
}

// SendCommand passes command to a given connection (adds logging and EOL character) and returns the response.
func (c *Client) SendCommand(cmd string) (string, error) {
	c.commandsCh <- utils.StringConcat(cmd, "\n")

	select {
	case resp := <-c.commandResponseCh:
		if resp == "" {
			cmdFirstLine := strings.SplitN(cmd, "\n", 2)[0]

			return "", fmt.Errorf("command error '%s': %w", cmdFirstLine, ErrEmptyResponse)
		}

		if strings.HasPrefix(resp, "ERROR:") {
			cmdFirstLine := strings.SplitN(cmd, "\n", 2)[0]
			c.logger.Warn(fmt.Sprintf("command error '%s': %s", cmdFirstLine, resp))
		}

		return resp, nil
	case <-time.After(10 * time.Second):
		cmdFirstLine := strings.SplitN(cmd, "\n", 2)[0]

		return "", fmt.Errorf("command error '%s': %w", cmdFirstLine, ErrTimeout)
	}
}

// SendCommandf passes command to a given connection (adds logging and EOL character) and returns the response.
func (c *Client) SendCommandf(format string, a ...any) (string, error) {
	return c.SendCommand(fmt.Sprintf(format, a...))
}

// rawCommand passes command to a given connection (adds logging and EOL character).
func (c *Client) rawCommand(cmd string) error {
	if c.logger.Enabled(context.Background(), slog.LevelDebug) {
		c.logger.Debug(cmd)
	}

	_, err := c.conn.Write([]byte(cmd))
	if err != nil {
		return fmt.Errorf("unable to write into OpenVPN management connection: %w", err)
	}

	return nil
}

// readMessage .
func (c *Client) readMessage() (string, error) {
	var (
		buf  bytes.Buffer
		line []byte
	)

	for {
		if ok := c.scanner.Scan(); !ok {
			if c.scanner.Err() != nil {
				return "", fmt.Errorf("readMessage: scanner error: %w", c.scanner.Err())
			}

			return "", nil
		}

		line = c.scanner.Bytes()

		if _, err := buf.Write(line); err != nil {
			return "", fmt.Errorf("readMessage: unable to write string to buffer: %w", err)
		}

		if _, err := buf.WriteString("\n"); err != nil {
			return "", fmt.Errorf("readMessage: unable to write newline to buffer: %w", err)
		}

		if c.isMessageLineEOF(line) {
			message := buf.String()
			if c.logger.Enabled(context.Background(), slog.LevelDebug) {
				c.logger.Debug(message)
			}

			return message, nil
		}
	}
}

func (c *Client) isMessageLineEOF(line []byte) bool {
	return bytes.HasPrefix(line, []byte(">CLIENT:ENV,END")) ||
		bytes.Index(line, []byte("END")) == 0 ||
		bytes.Index(line, []byte("SUCCESS:")) == 0 ||
		bytes.Index(line, []byte("ERROR:")) == 0 ||
		bytes.HasPrefix(line, []byte(">HOLD:")) ||
		bytes.HasPrefix(line, []byte(">INFO:")) ||
		bytes.HasPrefix(line, []byte(">NOTIFY:")) ||
		bytes.HasPrefix(line, []byte(":OpenVPN"))
}

func (c *Client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.closed {
		c.closed = true
		_ = c.conn.Close()
		close(c.commandsCh)
	}
}
