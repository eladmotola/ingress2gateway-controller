package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eladmotola/ingress2gateway-controller/pkg/config"
)

func TestMapFlag(t *testing.T) {
	mf := make(mapFlag)

	err := mf.Set("ingress-class-1=gateway-class-1")
	require.NoError(t, err)
	assert.Equal(t, "gateway-class-1", mf["ingress-class-1"])

	err = mf.Set("invalid-format")
	require.Error(t, err)

	assert.NotEmpty(t, mf.String())
}

func TestExternalGatewayMapFlag(t *testing.T) {
	egf := make(externalGatewayMapFlag)

	err := egf.Set("nginx-ext=gateway-infra/ext-gw")
	require.NoError(t, err)
	assert.Equal(t, config.ExternalGateway{Namespace: "gateway-infra", Name: "ext-gw"}, egf["nginx-ext"])

	// Test duplicate key returns error
	err = egf.Set("nginx-ext=other-ns/other-gw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate external gateway mapping")

	// Test invalid target format
	err = egf.Set("nginx-ext2=invalid")
	require.Error(t, err)

	str := egf.String()
	assert.Contains(t, str, "nginx-ext=gateway-infra/ext-gw")
}
