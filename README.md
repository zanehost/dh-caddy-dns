# DreamHost DNS module for Caddy

This package contains a DNS provider module for [Caddy](https://caddyserver.com).
It can be used to manage DNS records in [DreamHost](https://www.dreamhost.com) zones,
most commonly to solve the [ACME DNS challenge](https://caddyserver.com/docs/automatic-https#dns-challenge)
for wildcard or internal certificates.

It is a self-contained replacement for the unmaintained
[caddy-dns/dreamhost](https://github.com/caddy-dns/dreamhost) module: it implements the
current [libdns](https://github.com/libdns/libdns) 1.x interfaces (which modern Caddy
requires) and talks to the DreamHost API directly, with no third-party API client.

## Building

Build Caddy with this module using [xcaddy](https://github.com/caddyserver/xcaddy):

```sh
xcaddy build --with github.com/zanehost/dh-caddy-dns
```

## Credentials

Create an API key at <https://panel.dreamhost.com/?tree=home.api> with access to
**All dns functions** (or at least `dns-list_records`, `dns-add_record`, and
`dns-remove_record`).

## Config examples

Caddyfile, globally for all sites (recommended):

```caddyfile
{
	acme_dns dreamhost {env.DREAMHOST_API_KEY}
}
```

Caddyfile, per site:

```caddyfile
example.com {
	tls {
		dns dreamhost {env.DREAMHOST_API_KEY}
	}
}
```

The block form `dreamhost { api_key <key> }` is also accepted.

JSON, inside the `tls` app's automation policies:

```json
{
	"module": "acme",
	"challenges": {
		"dns": {
			"provider": {
				"name": "dreamhost",
				"api_key": "{env.DREAMHOST_API_KEY}"
			}
		}
	}
}
```

## DreamHost caveats

- **Propagation is slow.** DreamHost can take several minutes (occasionally much
  longer) to publish record changes to its nameservers. If challenges time out,
  raise the challenge timing in your `tls` config, e.g.:

  ```caddyfile
  example.com {
  	tls {
  		dns dreamhost {env.DREAMHOST_API_KEY}
  		propagation_delay 2m
  		propagation_timeout 30m
  		resolvers ns1.dreamhost.com
  	}
  }
  ```

- **TTLs are not configurable.** The DreamHost API has no TTL parameter, so TTLs
  on input records are ignored and records read back report a TTL of 0.
- **There is no update operation.** `SetRecords` is implemented as
  remove-then-add, so for a brief moment a changed record may be absent.
- The API only exposes records it considers *editable*; registrar/system records
  cannot be changed and this module reports an error if asked to replace one.
