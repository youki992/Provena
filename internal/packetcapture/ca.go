package packetcapture

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type CertificateAuthority struct {
	cert     *x509.Certificate
	key      *ecdsa.PrivateKey
	certPEM  []byte
	keyPEM   []byte
	certPath string
	keyPath  string
	mu       sync.Mutex
	leaf     map[string]*tls.Certificate
}

func LoadOrCreateCA(certPath, keyPath string) (*CertificateAuthority, error) {
	if strings.TrimSpace(certPath) == "" {
		certPath = filepath.Join("data", "packet_capture", "provena-ca.crt")
	}
	if strings.TrimSpace(keyPath) == "" {
		keyPath = filepath.Join("data", "packet_capture", "provena-ca.key")
	}
	certPath, _ = filepath.Abs(certPath)
	keyPath, _ = filepath.Abs(keyPath)
	if certData, certErr := os.ReadFile(certPath); certErr == nil {
		if keyData, keyErr := os.ReadFile(keyPath); keyErr == nil {
			certBlock, _ := pem.Decode(certData)
			keyBlock, _ := pem.Decode(keyData)
			if certBlock != nil && keyBlock != nil {
				cert, err := x509.ParseCertificate(certBlock.Bytes)
				if err == nil {
					key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
					if err == nil {
						return &CertificateAuthority{cert: cert, key: key, certPEM: certData, keyPEM: keyData, certPath: certPath, keyPath: keyPath, leaf: map[string]*tls.Certificate{}}, nil
					}
				}
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Provena Local Interception CA", Organization: []string{"Provena"}}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CertificateAuthority{cert: cert, key: key, certPEM: certPEM, keyPEM: keyPEM, certPath: certPath, keyPath: keyPath, leaf: map[string]*tls.Certificate{}}, nil
}

func (ca *CertificateAuthority) CertPEM() []byte {
	if ca == nil {
		return nil
	}
	return append([]byte(nil), ca.certPEM...)
}
func (ca *CertificateAuthority) CertPath() string {
	if ca == nil {
		return ""
	}
	return ca.certPath
}
func (ca *CertificateAuthority) KeyPath() string {
	if ca == nil {
		return ""
	}
	return ca.keyPath
}

func (ca *CertificateAuthority) Leaf(host string) (*tls.Certificate, error) {
	if ca == nil || ca.cert == nil || ca.key == nil {
		return nil, fmt.Errorf("CA 未初始化")
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("缺少证书主机名")
	}
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if cached := ca.leaf[host]; cached != nil {
		return cached, nil
	}
	serialBytes := sha256.Sum256([]byte(host + ca.cert.SerialNumber.String()))
	serial := new(big.Int).SetBytes(serialBytes[:])
	if serial.Sign() == 0 {
		serial = big.NewInt(1)
	}
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), mustMarshalECKey(key))
	if err != nil {
		return nil, err
	}
	ca.leaf[host] = &cert
	return &cert, nil
}

func mustMarshalECKey(key *ecdsa.PrivateKey) []byte {
	der, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}
