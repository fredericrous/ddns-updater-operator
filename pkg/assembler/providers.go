package assembler

// knownProviders lists the provider names shipped by ddns-updater at the time
// of writing (github.com/qdm12/ddns-updater, internal/provider/constants).
//
// It is advisory only. The operator never rejects a provider it does not
// recognise — ddns-updater is the authority, and a provider added upstream
// must keep working without an operator release. The list exists so an obvious
// typo ("cloudlfare") surfaces as a warning Event instead of a ConfigMap that
// silently breaks ddns-updater at startup.
//
// Refresh with:
//
//	grep -oE 'models\.Provider = "[a-z0-9._-]+"' \
//	  internal/provider/constants/providers.go | sed 's/.*"\(.*\)"/\1/'
var knownProviders = map[string]bool{
	"aliyun": true, "allinkl": true, "changeip": true, "cloudflare": true,
	"custom": true, "dd24": true, "ddnss": true, "desec": true,
	"digitalocean": true, "dnsomatic": true, "dnspod": true, "domeneshop": true,
	"dondominio": true, "dreamhost": true, "duckdns": true, "dyn": true,
	"dynu": true, "dynv6": true, "easydns": true, "example": true,
	"freedns": true, "gandi": true, "gcp": true, "godaddy": true,
	"goip": true, "he": true, "hetzner": true, "infomaniak": true,
	"inwx": true, "ionos": true, "linode": true, "loopia": true,
	"luadns": true, "myaddr": true, "name.com": true, "namecheap": true,
	"namesilo": true, "netcup": true, "njalla": true, "noip": true,
	"nowdns": true, "opendns": true, "ovh": true, "porkbun": true,
	"route53": true, "scaleway": true, "selfhost.de": true, "servercow": true,
	"spdyn": true, "strato": true, "variomedia": true, "vultr": true,
	"zoneedit": true,
}

// IsKnownProvider reports whether name is a provider this operator has seen
// upstream. A false result is not an error — see knownProviders.
func IsKnownProvider(name string) bool {
	return knownProviders[name]
}
