package telemetry

import (
	"net/url"
	"strconv"
)

func validEndpointPort(endpoint *url.URL) bool {
	port := endpoint.Port()
	if port == "" {
		return true
	}
	parsed, err := strconv.ParseUint(port, 10, 16)
	return err == nil && parsed != 0
}
