package dreamhost

import (
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func init() {
	caddy.RegisterModule(new(Provider))
}

// CaddyModule returns the Caddy module information.
func (*Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "dns.providers.dreamhost",
		New: func() caddy.Module { return new(Provider) },
	}
}

// Provision sets up the module by expanding placeholders
// (such as {env.DREAMHOST_API_KEY}) in its configuration.
func (p *Provider) Provision(ctx caddy.Context) error {
	repl := caddy.NewReplacer()
	p.APIKey = repl.ReplaceAll(p.APIKey, "")
	p.APIURL = repl.ReplaceAll(p.APIURL, "")
	return nil
}

// UnmarshalCaddyfile sets up the DNS provider from Caddyfile tokens. Syntax:
//
//	dreamhost [<api_key>] {
//	    api_key <api_key>
//	}
func (p *Provider) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		if d.NextArg() {
			p.APIKey = d.Val()
		}
		if d.NextArg() {
			return d.ArgErr()
		}
		for nesting := d.Nesting(); d.NextBlock(nesting); {
			switch d.Val() {
			case "api_key":
				if p.APIKey != "" {
					return d.Err("API key already set")
				}
				if !d.NextArg() {
					return d.ArgErr()
				}
				p.APIKey = d.Val()
				if d.NextArg() {
					return d.ArgErr()
				}
			default:
				return d.Errf("unrecognized subdirective '%s'", d.Val())
			}
		}
	}
	if p.APIKey == "" {
		return d.Err("missing API key")
	}
	return nil
}

// Interface guards
var (
	_ caddyfile.Unmarshaler = (*Provider)(nil)
	_ caddy.Provisioner     = (*Provider)(nil)
)
