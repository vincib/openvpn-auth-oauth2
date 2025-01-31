package config

import (
	"fmt"
	"net/url"
	"strings"
	"text/template"
	"time"
)

type Config struct {
	ConfigFile string  `koanf:"config"`
	Log        Log     `koanf:"log"`
	HTTP       HTTP    `koanf:"http"`
	OpenVpn    OpenVpn `koanf:"openvpn"`
	OAuth2     OAuth2  `koanf:"oauth2"`
}

type HTTP struct {
	Listen             string             `koanf:"listen"`
	CertFile           string             `koanf:"cert"`
	KeyFile            string             `koanf:"key"`
	TLS                bool               `koanf:"tls"`
	BaseURL            *url.URL           `koanf:"baseurl"`
	Secret             string             `koanf:"secret"`
	CallbackTemplate   *template.Template `koanf:"template"`
	Check              HTTPCheck          `koanf:"check"`
	EnableProxyHeaders bool               `koanf:"enable-proxy-headers"`
}

type HTTPCheck struct {
	IPAddr bool `koanf:"ipaddr"`
}

type Log struct {
	Format string `koanf:"format"`
	Level  string `koanf:"level"`
}

type OpenVpn struct {
	Addr               *url.URL          `koanf:"addr"`
	Password           string            `koanf:"password"`
	Bypass             OpenVpnBypass     `koanf:"bypass"`
	AuthTokenUser      bool              `koanf:"auth-token-user"`
	AuthPendingTimeout time.Duration     `koanf:"auth-pending-timeout"`
	CommonName         OpenVPNCommonName `koanf:"common-name"`
}

type OpenVpnBypass struct {
	CommonNames []string `koanf:"cn"`
}

type OpenVPNCommonName struct {
	Mode OpenVPNCommonNameMode `koanf:"mode"`
}

type OAuth2 struct {
	Issuer          *url.URL        `koanf:"issuer"`
	Provider        string          `koanf:"provider"`
	AuthorizeParams string          `koanf:"authorize-params"`
	Endpoints       OAuth2Endpoints `koanf:"endpoint"`
	Client          OAuth2Client    `koanf:"client"`
	Scopes          []string        `koanf:"scopes"`
	Pkce            bool            `koanf:"pkce"`
	Validate        OAuth2Validate  `koanf:"validate"`
}

type OAuth2Client struct {
	ID     string `koanf:"id"`
	Secret string `koanf:"secret"`
}

type OAuth2Endpoints struct {
	Discovery *url.URL `koanf:"discovery"`
	Auth      *url.URL `koanf:"auth"`
	Token     *url.URL `koanf:"token"`
}

type OAuth2Validate struct {
	Groups     []string `koanf:"groups"`
	Roles      []string `koanf:"roles"`
	IPAddr     bool     `koanf:"ipaddr"`
	Issuer     bool     `koanf:"issuer"`
	CommonName string   `koanf:"common_name"`
}

type OpenVPNCommonNameMode int

const (
	CommonNameModePlain OpenVPNCommonNameMode = iota
	CommonNameModeOmit
	CommonNameModeOmitValue = "-"
)

//goland:noinspection GoMixedReceiverTypes
func (s OpenVPNCommonNameMode) String() string {
	text, _ := s.MarshalText()

	return string(text)
}

//goland:noinspection GoMixedReceiverTypes
func (s OpenVPNCommonNameMode) MarshalText() ([]byte, error) {
	switch s {
	case CommonNameModePlain:
		return []byte("plain"), nil
	case CommonNameModeOmit:
		return []byte("omit"), nil
	default:
		return nil, fmt.Errorf("unknown identitfer %d", s)
	}
}

//goland:noinspection GoMixedReceiverTypes
func (s *OpenVPNCommonNameMode) UnmarshalText(text []byte) error {
	config := strings.ToLower(string(text))
	switch config {
	case "plain":
		*s = CommonNameModePlain
	case "omit":
		*s = CommonNameModeOmit
	default:
		return fmt.Errorf("invalid value %s", config)
	}

	return nil
}
