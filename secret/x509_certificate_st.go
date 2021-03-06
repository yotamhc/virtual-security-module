// Copyright © 2017 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause
package secret

import (
	gocontext "context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"path"
	"time"

	"github.com/vmware/virtual-security-module/config"
	"github.com/vmware/virtual-security-module/context"
	"github.com/vmware/virtual-security-module/crypt"
	"github.com/vmware/virtual-security-module/model"
	"github.com/vmware/virtual-security-module/util"
	"github.com/vmware/virtual-security-module/vds"
	"github.com/vmware/virtual-security-module/vks"
)

const X509CertificateSecretTypeName = "X509Certificate"

func init() {
	if err := SecretTypeRegistrar.Register(X509CertificateSecretTypeName, NewX509CertificateSecretType()); err != nil {
		panic(fmt.Sprintf("Failed to register secret type %v: %v", X509CertificateSecretTypeName, err))
	}
}

type X509CertificateSecretType struct {
	dataStore    vds.DataStoreAdapter
	keyStore     *vks.VirtualKeyStore
	authzManager context.AuthorizationManager
	cfg          *config.Config
}

type X509CertificateSecretMetaData struct {
	CommonName         string `json:"commonName"`
	Organization       string `json:"organization"`
	OrganizationalUnit string `json:"organizationalUnit"`
	Country            string `json:"country"`
	Locality           string `json:"locality"`
	PrivateKeyId       string `json:"privateKeyId"`
}

func NewX509CertificateSecretType() *X509CertificateSecretType {
	return &X509CertificateSecretType{}
}

func (certST *X509CertificateSecretType) Type() string {
	return X509CertificateSecretTypeName
}

func (certST *X509CertificateSecretType) Init(moduleInitContext *context.ModuleInitContext) error {
	certST.dataStore = moduleInitContext.DataStore
	certST.keyStore = moduleInitContext.VirtualKeyStore
	certST.authzManager = moduleInitContext.AuthzManager
	certST.cfg = moduleInitContext.Config

	return nil
}

func (certST *X509CertificateSecretType) CreateSecret(ctx gocontext.Context, secretEntry *model.SecretEntry) (string, error) {
	// get certificate meta-data
	var certMetaData X509CertificateSecretMetaData
	if err := json.Unmarshal([]byte(secretEntry.MetaData), &certMetaData); err != nil {
		return "", util.ErrInputValidation
	}

	// generate encryption key for secret
	key, err := crypt.GenerateKey()
	if err != nil {
		return "", util.ErrInternal
	}

	// reduce key exposure due to memory compromize / leak
	defer util.Memzero(key)

	// generate secret data (certificate in this case) and encrypt it using key
	certPEM, err := certST.generateCert(ctx, &certMetaData)
	if err != nil {
		return "", err
	}

	encryptedSecretData, err := crypt.Encrypt(certPEM, key)
	if err != nil {
		return "", util.ErrInternal
	}

	se := model.NewSecretEntry(secretEntry)
	se.SecretData = encryptedSecretData

	// create a data store entry and save it
	dataStoreEntry, err := vds.SecretEntryToDataStoreEntry(se)
	if err != nil {
		return "", err
	}
	if err := certST.dataStore.CreateEntry(dataStoreEntry); err != nil {
		return "", err
	}

	// persist key using virtual key store
	secretPath := vds.SecretIdToPath(secretEntry.Id)
	if err := certST.keyStore.Create(secretPath, key); err != nil {
		return "", err
	}

	return secretEntry.Id, nil
}

func (certST *X509CertificateSecretType) GetSecret(ctx gocontext.Context, secretEntry *model.SecretEntry) (*model.SecretEntry, error) {
	secretPath := vds.SecretIdToPath(secretEntry.Id)

	// fetch encryption key
	key, err := certST.keyStore.Read(secretPath)
	if err != nil {
		return nil, err
	}

	// reduce key exposure due to memory compromize / leak
	defer util.Memzero(key)

	// decrypt secret data using key
	certPEM, err := crypt.Decrypt(secretEntry.SecretData, key)
	if err != nil {
		return nil, util.ErrInternal
	}

	// set decrypted data
	secretEntry.SecretData = certPEM

	return secretEntry, nil
}

func (certST *X509CertificateSecretType) DeleteSecret(ctx gocontext.Context, secretEntry *model.SecretEntry) error {
	secretPath := vds.SecretIdToPath(secretEntry.Id)

	if err := certST.dataStore.DeleteEntry(secretPath); err != nil {
		return err
	}

	if err := certST.keyStore.Delete(secretPath); err != nil {
		return err
	}

	return nil
}

func (certST *X509CertificateSecretType) generateCert(ctx gocontext.Context, certMetaData *X509CertificateSecretMetaData) ([]byte, error) {
	if certMetaData.PrivateKeyId == "" {
		return []byte{}, util.ErrInputValidation
	}

	privKey, err := certST.getSubjectPrivKey(ctx, certMetaData.PrivateKeyId)
	if err != nil {
		return []byte{}, err
	}

	serialNumber, err := getCertSerialNumber()
	if err != nil {
		return []byte{}, err
	}

	subject, err := getCertSubject(certMetaData)
	if err != nil {
		return []byte{}, err
	}

	template := getCertTemplate(serialNumber, subject)

	caCert, caPrivKey, err := getCACertAndKey(certST.cfg)
	if err != nil {
		return []byte{}, err
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, privKey.Public(), caPrivKey)
	if err != nil {
		return []byte{}, err
	}

	block := pem.Block{Type: "CERTIFICATE", Bytes: certDER}
	return pem.EncodeToMemory(&block), nil
}

func (certST *X509CertificateSecretType) getSubjectPrivKey(ctx gocontext.Context, privKeyId string) (*rsa.PrivateKey, error) {
	privKeyPath := vds.SecretIdToPath(privKeyId)

	// verify that the caller has access to the private key
	if err := certST.authzManager.Allowed(ctx, model.Operation{Label: model.OpRead}, path.Dir(privKeyPath)); err != nil {
		return nil, err
	}

	dataStoreEntry, err := certST.dataStore.ReadEntry(privKeyPath)
	if err != nil {
		return nil, err
	}

	secretEntry, err := vds.DataStoreEntryToSecretEntry(dataStoreEntry)
	if err != nil {
		return nil, err
	}

	key, err := certST.keyStore.Read(privKeyPath)
	if err != nil {
		return nil, err
	}

	defer util.Memzero(key)

	pkPEM, err := crypt.Decrypt(secretEntry.SecretData, key)
	if err != nil {
		return nil, util.ErrInternal
	}

	block, _ := pem.Decode(pkPEM)
	if block == nil {
		return nil, util.ErrInternal
	}

	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func getCertSubject(certMetaData *X509CertificateSecretMetaData) (*pkix.Name, error) {
	if certMetaData.CommonName == "" || certMetaData.Organization == "" {
		return nil, util.ErrInputValidation
	}

	return &pkix.Name{
		CommonName:         certMetaData.CommonName,
		Organization:       []string{certMetaData.Organization},
		OrganizationalUnit: []string{certMetaData.OrganizationalUnit},
		Country:            []string{certMetaData.Country},
		Locality:           []string{certMetaData.Locality},
	}, nil
}

func getCertSerialNumber() (*big.Int, error) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, serialNumberLimit)
}

func getCertTemplate(serialNumber *big.Int, subject *pkix.Name) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:       serialNumber,
		Subject:            *subject,
		SignatureAlgorithm: x509.SHA256WithRSA,
		NotBefore:          time.Now(),
		NotAfter:           time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:           x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
}

func getCACertAndKey(cfg *config.Config) (*x509.Certificate, *rsa.PrivateKey, error) {
	caCertFile := cfg.HttpsConfig.CaCert
	caKeyFile := cfg.HttpsConfig.CaKey

	if caCertFile == "" || caKeyFile == "" {
		return nil, nil, util.ErrInputValidation
	}

	caCert, err := util.ReadCertificate(caCertFile)
	if err != nil {
		return nil, nil, err
	}

	caPrivKey, err := util.ReadRSAPrivateKey(caKeyFile)
	if err != nil {
		return nil, nil, err
	}

	return caCert, caPrivKey, nil
}
