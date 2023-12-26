// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mtls provides mTLS and TLS config support.
package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
)

var (
	ErrMissingCertOrKey       = errors.New("root ca set without certificate or key")
	ErrNoValidRootCertificate = errors.New("no valid root certificate")
	ErrMissingCertificate     = errors.New("missing certificate")
	ErrMissingKey             = errors.New("missing key")
)

// NewServerConfig returns a TLS configuration for use with an mTLS or TLS connection.
// If a root CA PEM block is provided the configuration will be set up for mTLS,
// otherwise if it is empty the config will be for TLS connections. If all
// parameters are empty, a nil config will be returned.
func NewServerConfig(rootPEM, certPEMBlock, keyPEMBlock []byte) (*tls.Config, error) {
	return newConfig(true, rootPEM, certPEMBlock, keyPEMBlock)
}

// NewClientConfig returns a TLS configuration for use with an mTLS or TLS connection.
// If all parameters are empty, a nil config will be returned.
func NewClientConfig(rootPEM, certPEMBlock, keyPEMBlock []byte) (*tls.Config, error) {
	return newConfig(false, rootPEM, certPEMBlock, keyPEMBlock)
}

func newConfig(server bool, rootPEM, certPEMBlock, keyPEMBlock []byte) (*tls.Config, error) {
	if len(rootPEM) == 0 && len(certPEMBlock) == 0 && len(keyPEMBlock) == 0 {
		return nil, nil
	}
	var tlsConfig tls.Config
	if len(rootPEM) != 0 {
		if len(certPEMBlock) == 0 || len(keyPEMBlock) == 0 {
			return nil, ErrMissingCertOrKey
		}
		caPool := x509.NewCertPool()
		ok := caPool.AppendCertsFromPEM(rootPEM)
		if !ok {
			return nil, ErrNoValidRootCertificate
		}
		if server {
			tlsConfig.ClientCAs = caPool
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		} else {
			tlsConfig.RootCAs = caPool
		}
	}
	if len(certPEMBlock) != 0 || len(keyPEMBlock) != 0 {
		if len(certPEMBlock) == 0 {
			return nil, ErrMissingCertificate
		}
		if len(keyPEMBlock) == 0 {
			return nil, ErrMissingKey
		}
		cert, err := tls.X509KeyPair(certPEMBlock, keyPEMBlock)
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	return &tlsConfig, nil
}
