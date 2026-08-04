package chatlifecycle

import (
	"fmt"
	"net"
	"net/url"
	pathpkg "path"
	"strconv"
	"strings"
)

func validateLocalObservationShape(o ObservationConfig) error {
	roles := []struct {
		path  string
		count int
	}{
		{"observation.service_nodes", len(o.ServiceNodes)},
		{"observation.workers", len(o.Workers)},
		{"observation.host_metrics", len(o.HostMetrics)},
		{"observation.api_addrs", len(o.APIAddrs)},
		{"observation.gateway_tcp_addrs", len(o.GatewayTCPAddrs)},
	}
	for _, role := range roles {
		if role.count != formalWorkers {
			return fieldError(role.path, "must contain exactly 3 entries for local baseline")
		}
	}
	return nil
}

func validateObservation(o ObservationConfig) error {
	if o.Cadence <= 0 {
		return fieldError("observation.cadence", "must be greater than zero")
	}
	if err := validateEndpointRole("observation.service_nodes", o.ServiceNodes); err != nil {
		return err
	}
	if err := validateEndpointRole("observation.workers", o.Workers); err != nil {
		return err
	}
	if err := validateEndpointRole("observation.host_metrics", o.HostMetrics); err != nil {
		return err
	}
	if err := validateHTTPAddressPool("observation.api_addrs", o.APIAddrs); err != nil {
		return err
	}
	if err := validateGatewayAddressPool("observation.gateway_tcp_addrs", o.GatewayTCPAddrs); err != nil {
		return err
	}
	if err := validateCrossRoleEndpointDuplicates(o); err != nil {
		return err
	}
	for gatewayIndex, gateway := range o.GatewayTCPAddrs {
		gatewayKey, _ := parseGatewayEndpoint(gateway)
		for apiIndex, api := range o.APIAddrs {
			apiKey, _ := parseHTTPEndpoint(api)
			if gatewayKey == apiKey.authority {
				return fieldError(fmt.Sprintf("observation.gateway_tcp_addrs[%d]", gatewayIndex), fmt.Sprintf("aliases observation.api_addrs[%d]", apiIndex))
			}
		}
	}
	return nil
}

func validateEndpointRole(path string, endpoints []EndpointDeclaration) error {
	if len(endpoints) == 0 {
		return fieldError(path, "must not be empty")
	}
	seenNames := make(map[string]int, len(endpoints))
	seenAddresses := make(map[string]int, len(endpoints))
	for i, endpoint := range endpoints {
		name := strings.TrimSpace(endpoint.Name)
		if name == "" {
			return fieldError(fmt.Sprintf("%s[%d].name", path, i), "is required")
		}
		if previous, ok := seenNames[name]; ok {
			return fieldError(fmt.Sprintf("%s[%d].name", path, i), fmt.Sprintf("duplicates %s[%d].name", path, previous))
		}
		address := strings.TrimSpace(endpoint.Address)
		if address == "" {
			return fieldError(fmt.Sprintf("%s[%d].address", path, i), "is required")
		}
		parsed, reason := parseHTTPEndpoint(address)
		if reason != "" {
			return fieldError(fmt.Sprintf("%s[%d].address", path, i), reason)
		}
		if previous, ok := seenAddresses[parsed.key]; ok {
			return fieldError(fmt.Sprintf("%s[%d].address", path, i), fmt.Sprintf("duplicates %s[%d].address", path, previous))
		}
		seenNames[name], seenAddresses[parsed.key] = i, i
	}
	return nil
}

func validateHTTPAddressPool(path string, addresses []string) error {
	if len(addresses) == 0 {
		return fieldError(path, "must not be empty")
	}
	seen := make(map[string]int, len(addresses))
	for i, raw := range addresses {
		address := strings.TrimSpace(raw)
		if address == "" {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), "is required")
		}
		parsed, reason := parseHTTPEndpoint(address)
		if reason != "" {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), reason)
		}
		if previous, ok := seen[parsed.key]; ok {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), fmt.Sprintf("duplicates %s[%d]", path, previous))
		}
		seen[parsed.key] = i
	}
	return nil
}

func validateGatewayAddressPool(path string, addresses []string) error {
	if len(addresses) == 0 {
		return fieldError(path, "must not be empty")
	}
	seen := make(map[string]int, len(addresses))
	for i, raw := range addresses {
		if strings.TrimSpace(raw) == "" {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), "is required")
		}
		key, reason := parseGatewayEndpoint(raw)
		if reason != "" {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), reason)
		}
		if previous, ok := seen[key]; ok {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), fmt.Sprintf("duplicates %s[%d]", path, previous))
		}
		seen[key] = i
	}
	return nil
}

func validateCrossRoleEndpointDuplicates(o ObservationConfig) error {
	roles := []struct {
		path      string
		endpoints []EndpointDeclaration
	}{
		{"observation.service_nodes", o.ServiceNodes},
		{"observation.workers", o.Workers},
		{"observation.host_metrics", o.HostMetrics},
	}
	seenNames := make(map[string]string)
	seenAddresses := make(map[string]string)
	for _, role := range roles {
		for index, endpoint := range role.endpoints {
			namePath := fmt.Sprintf("%s[%d].name", role.path, index)
			name := strings.TrimSpace(endpoint.Name)
			if previous, ok := seenNames[name]; ok {
				return fieldError(namePath, "duplicates "+previous)
			}
			seenNames[name] = namePath
			addressPath := fmt.Sprintf("%s[%d].address", role.path, index)
			address, _ := parseHTTPEndpoint(endpoint.Address)
			if previous, ok := seenAddresses[address.key]; ok {
				return fieldError(addressPath, "duplicates "+previous)
			}
			seenAddresses[address.key] = addressPath
		}
	}
	return nil
}

type httpEndpointKey struct {
	key       string
	authority string
}

func parseHTTPEndpoint(raw string) (httpEndpointKey, string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		if strings.Contains(err.Error(), "invalid port") {
			return httpEndpointKey{}, "port must be a number in 1..65535"
		}
		return httpEndpointKey{}, "must be a valid absolute HTTP URL"
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" || !parsed.IsAbs() {
		return httpEndpointKey{}, "must be a valid absolute HTTP URL"
	}
	if scheme != "http" && scheme != "https" {
		return httpEndpointKey{}, "scheme must be http or https"
	}
	if parsed.User != nil {
		return httpEndpointKey{}, "must not include userinfo"
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return httpEndpointKey{}, "must not include a query"
	}
	if parsed.Fragment != "" {
		return httpEndpointKey{}, "must not include a fragment"
	}
	canonicalHost, reason := canonicalEndpointHost(parsed.Hostname())
	if reason != "" {
		return httpEndpointKey{}, reason
	}
	port := parsed.Port()
	if strings.HasSuffix(parsed.Host, ":") {
		return httpEndpointKey{}, "port must be a number in 1..65535"
	}
	if port == "" {
		if scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return httpEndpointKey{}, "port must be a number in 1..65535"
	}
	authority := net.JoinHostPort(canonicalHost, strconv.Itoa(portNumber))
	basePath := pathpkg.Clean(parsed.EscapedPath())
	if basePath == "." || basePath == "/" {
		basePath = ""
	}
	return httpEndpointKey{
		key:       scheme + "://" + authority + basePath,
		authority: authority,
	}, ""
}

func parseGatewayEndpoint(raw string) (string, string) {
	address := strings.TrimSpace(raw)
	if strings.Contains(address, "://") {
		return "", "must be a TCP host:port"
	}
	if strings.Contains(address, "@") {
		return "", "must not include userinfo"
	}
	if strings.ContainsAny(address, "/?#") {
		return "", "must not include a path, query, or fragment"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", "must be a TCP host:port"
	}
	canonicalHost, reason := canonicalEndpointHost(host)
	if reason != "" {
		return "", reason
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", "port must be a number in 1..65535"
	}
	return net.JoinHostPort(canonicalHost, strconv.Itoa(portNumber)), ""
}

func canonicalEndpointHost(host string) (string, string) {
	if strings.Contains(host, ":") {
		ipHost, zone, hasZone := strings.Cut(host, "%")
		if ip := net.ParseIP(ipHost); ip != nil && (!hasZone || zone != "") {
			if hasZone {
				return ip.String() + "%" + zone, ""
			}
			return ip.String(), ""
		}
		return "", "host must be a valid IP address"
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" || host == "." {
		return "", "host is required"
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), ""
	}
	return strings.ToLower(host), ""
}
