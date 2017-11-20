package certsign

import "github.com/mbaynton/SimplePuppetProvisioner/lib"

type CertSigner struct {
	appConfig lib.AppConfig
}

func NewCertSigner(config lib.AppConfig) *CertSigner {
	certSigner := CertSigner{appConfig: config}
	return &certSigner
}
