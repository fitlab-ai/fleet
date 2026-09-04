//go:build !darwin

package platform

func NewProxyManager() ProxyManager { return NoopProxy{} }
