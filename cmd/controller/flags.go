package main

import (
	"fmt"
	"strings"

	"github.com/eladmotola/ingress2gateway-controller/pkg/config"
)

type mapFlag map[string]string

func (m mapFlag) String() string {
	return fmt.Sprintf("%v", map[string]string(m))
}

func (m mapFlag) Set(value string) error {
	kv := strings.SplitN(value, "=", 2)
	if len(kv) != 2 {
		return fmt.Errorf("invalid format, use key=value")
	}
	m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	return nil
}

type externalGatewayMapFlag map[string]config.ExternalGateway

func (e externalGatewayMapFlag) String() string {
	var parts []string
	for cls, eg := range e {
		parts = append(parts, fmt.Sprintf("%s=%s/%s", cls, eg.Namespace, eg.Name))
	}
	return strings.Join(parts, "; ")
}

func (e externalGatewayMapFlag) Set(value string) error {
	cls, eg, err := config.ParseExternalGatewayMapping(value)
	if err != nil {
		return err
	}
	if _, exists := e[cls]; exists {
		return fmt.Errorf("duplicate external gateway mapping for ingress class %q", cls)
	}
	e[cls] = eg
	return nil
}
