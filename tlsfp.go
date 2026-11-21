package main

import (
	"crypto/tls"
	"net"
	"time"

	utls "github.com/refraction-networking/utls"
)

// helloSpec mirrors the ClientHello of nodecode.exe byte for byte
// (engine/provider.lisp: cl+ssl + OpenSSL defaults: TLS 1.2 only, 19 suite
// order, no SNI, no ALPN, no supported_versions). api.nodecode.ai gates free
// keys on this fingerprint; any other TLS stack is refused with
// free_needs_nodecode even when headers, body and UA are identical.
func helloSpec() *utls.ClientHelloSpec {
	return &utls.ClientHelloSpec{
		TLSVersMin:         utls.VersionTLS12,
		TLSVersMax:         utls.VersionTLS12,
		CompressionMethods: []byte{0},
		CipherSuites: []uint16{
			0xc02c, 0xc02b, 0xc030, 0xc02f, 0xc024, 0xc023, 0xc028, 0xc027,
			0xc00a, 0xc009, 0xc014, 0xc013, 0x009d, 0x009c, 0x003d, 0x003c,
			0x0035, 0x002f, 0x000a,
		},
		Extensions: []utls.TLSExtension{
			&utls.StatusRequestExtension{},
			&utls.SupportedCurvesExtension{Curves: []utls.CurveID{utls.X25519, utls.CurveP256, utls.CurveP384}},
			&utls.SupportedPointsExtension{SupportedPoints: []byte{0}},
			&utls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: []utls.SignatureScheme{
				0x0804, 0x0805, 0x0806, 0x0401, 0x0501, 0x0201, 0x0403, 0x0503, 0x0203, 0x0202, 0x0601, 0x0603,
			}},
			&utls.SessionTicketExtension{},
			&utls.ExtendedMasterSecretExtension{},
			&utls.RenegotiationInfoExtension{Renegotiation: utls.RenegotiateOnceAsClient},
		},
	}
}

// dial opens one TLS connection carrying that fingerprint. The CDN answer
// presents a *.b-cdn.net certificate, so verification is skipped; the
// hostname is still set for the (absent) SNI extension and the Host header.
func dial(addr string) (*utls.UConn, error) {
	raw, err := net.DialTimeout("tcp", addr, 20*time.Second)
	if err != nil {
		return nil, err
	}
	u := utls.UClient(raw, &utls.Config{
		ServerName:         hostOnly(addr),
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
	}, utls.HelloCustom)
	if err := u.ApplyPreset(helloSpec()); err != nil {
		raw.Close()
		return nil, err
	}
	if err := u.Handshake(); err != nil {
		raw.Close()
		return nil, err
	}
	return u, nil
}

func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}
