// Copyright 2020 Nokia
// Licensed under the BSD 3-Clause License.
// SPDX-License-Identifier: BSD-3-Clause

package cert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/cloudflare/cfssl/api/generator"
	"github.com/cloudflare/cfssl/cli/genkey"
	"github.com/cloudflare/cfssl/config"
	"github.com/cloudflare/cfssl/csr"
	"github.com/cloudflare/cfssl/initca"
	"github.com/cloudflare/cfssl/signer"
	"github.com/cloudflare/cfssl/signer/universal"
	log "github.com/sirupsen/logrus"
	"github.com/srl-labs/containerlab/nodes"
	"github.com/srl-labs/containerlab/types"
	"github.com/srl-labs/containerlab/utils"
)

type Certificates struct {
	Key  []byte
	Csr  []byte
	Cert []byte
}

// CertInput struct
type CertInput struct {
	Hosts            []string
	CommonName       string
	Country          string
	Locality         string
	Organization     string
	OrganizationUnit string
	Expiry           string

	Name     string
	LongName string
	Fqdn     string
	Prefix   string
}

// CaRootInput struct
type CaRootInput struct {
	CommonName       string
	Country          string
	Locality         string
	Organization     string
	OrganizationUnit string
	Expiry           string

	Prefix string
	Names  map[string]string // Not used right now
	// prefix for certificate/key file name
	NamePrefix string
}

var rootCACSRTempl string = `{
    "CN": "{{.Prefix}} Root CA",
    "key": {
       "algo": "rsa",
       "size": 2048
    },
    "names": [{
       "C": "BE",
       "L": "Antwerp",
       "O": "Nokia",
       "OU": "Container lab"
    }],
    "ca": {
       "expiry": "262800h"
    }
}
`

var NodeCSRTempl string = `{
    "CN": "{{.Name}}.{{.Prefix}}.io",
    "key": {
      "algo": "rsa",
      "size": 2048
    },
    "names": [{
      "C": "BE",
      "L": "Antwerp",
      "O": "Nokia",
      "OU": "Container lab"
    }],
    "hosts": [
      "{{.Name}}",
      "{{.LongName}}",
      "{{.Fqdn}}"
    ]
}
`

// GenerateRootCa function
func GenerateRootCa(labCARoot string, csrRootJsonTpl *template.Template, input CaRootInput) (*Certificates, error) {
	log.Info("Creating root CA")
	// create root CA root directory
	utils.CreateDirectory(labCARoot, 0755)
	var err error
	csrBuff := new(bytes.Buffer)
	err = csrRootJsonTpl.Execute(csrBuff, input)
	if err != nil {
		return nil, err
	}
	req := csr.CertificateRequest{
		KeyRequest: csr.NewKeyRequest(),
	}
	err = json.Unmarshal(csrBuff.Bytes(), &req)
	if err != nil {
		return nil, err
	}

	var key, csrPEM, cert []byte
	cert, csrPEM, key, err = initca.New(&req)
	if err != nil {
		return nil, err
	}
	certs := &Certificates{
		Key:  key,
		Csr:  csrPEM,
		Cert: cert,
	}
	writeCertFiles(certs, filepath.Join(labCARoot, input.NamePrefix))
	return certs, nil
}

// GenerateCert generates and signs a certificate passed as input and saves the certificate and generated private key by path
// CA used to sign the cert is passed as ca and caKey file paths
func GenerateCert(ca, caKey string, csrJSONTpl *template.Template, input CertInput, targetPath string) (*Certificates, error) {
	utils.CreateDirectory(targetPath, 0755)
	var err error
	csrBuff := new(bytes.Buffer)
	err = csrJSONTpl.Execute(csrBuff, input)
	if err != nil {
		return nil, err
	}

	req := &csr.CertificateRequest{
		KeyRequest: csr.NewKeyRequest(),
	}
	err = json.Unmarshal(csrBuff.Bytes(), req)
	if err != nil {
		return nil, err
	}

	var key, csrBytes []byte
	gen := &csr.Generator{Validator: genkey.Validator}
	csrBytes, key, err = gen.ProcessRequest(req)
	if err != nil {
		return nil, err
	}

	policy := &config.Signing{
		Profiles: map[string]*config.SigningProfile{},
		Default:  config.DefaultConfig(),
	}
	root := universal.Root{
		Config: map[string]string{
			"cert-file": ca,
			"key-file":  caKey,
		},
		ForceRemote: false,
	}
	s, err := universal.NewSigner(root, policy)
	if err != nil {
		return nil, err
	}

	var cert []byte
	signReq := signer.SignRequest{
		Request: string(csrBytes),
	}
	cert, err = s.Sign(signReq)
	if err != nil {
		return nil, err
	}
	if len(signReq.Hosts) == 0 && len(req.Hosts) == 0 {
		log.Warning(generator.CSRNoHostMessage)
	}
	certs := &Certificates{
		Key:  key,
		Csr:  csrBytes,
		Cert: cert,
	}

	writeCertFiles(certs, filepath.Join(targetPath, input.Name))
	return certs, nil
}

// RetrieveNodeCertData reads the node private key and certificate by the well known paths
// if either of those files doesn't exist, an error is returned
func RetrieveNodeCertData(n *types.NodeConfig, labCADir string) (*Certificates, error) {
	var nodeCertFilesDir = filepath.Join(labCADir, n.ShortName)
	var nodeCertFile = filepath.Join(nodeCertFilesDir, n.ShortName+".pem")
	var nodeKeyFile = filepath.Join(nodeCertFilesDir, n.ShortName+"-key.pem")

	var certs = &Certificates{}

	var err error
	stat, err := os.Stat(nodeCertFilesDir)
	// the directory for the nodes certificates doesn't exist
	if err != nil || !stat.IsDir() {
		return nil, err
	}

	certs.Cert, err = utils.ReadFileContent(nodeCertFile)
	if err != nil {
		return nil, err
	}

	certs.Key, err = utils.ReadFileContent(nodeKeyFile)
	if err != nil {
		return nil, err
	}

	return certs, nil
}

func writeCertFiles(certs *Certificates, filesPrefix string) {
	utils.CreateFile(filesPrefix+".pem", string(certs.Cert))
	utils.CreateFile(filesPrefix+"-key.pem", string(certs.Key))
	utils.CreateFile(filesPrefix+".csr", string(certs.Csr))
}

//CreateRootCA creates RootCA key/certificate if it is needed by the topology
func CreateRootCA(configName, labCARoot string, ns map[string]nodes.Node) error {
	rootCANeeded := false
	// check if srl kinds defined in topo
	// for them we need to create rootCA and certs
	for _, n := range ns {
		if n.Config().Kind == "srl" {
			rootCANeeded = true
			break
		}
	}

	if !rootCANeeded {
		return nil
	}

	var rootCaCertPath = filepath.Join(labCARoot, "root-ca.pem")
	var rootCaKeyPath = filepath.Join(labCARoot, "root-ca-key.pem")

	var rootCaCertExists = false
	var rootCaKeyExists = false

	_, err := os.Stat(rootCaCertPath)
	if err == nil {
		rootCaCertExists = true
	}
	_, err = os.Stat(rootCaKeyPath)
	if err == nil {
		rootCaKeyExists = true
	}
	// if both files exist skip root CA creation
	if rootCaCertExists && rootCaKeyExists {
		rootCANeeded = false
	}
	if !rootCANeeded {
		return nil
	}

	tpl, err := template.New("ca-csr").Parse(rootCACSRTempl)
	if err != nil {
		return fmt.Errorf("failed to parse Root CA CSR Template: %v", err)
	}
	rootCerts, err := GenerateRootCa(labCARoot, tpl, CaRootInput{
		Prefix:     configName,
		NamePrefix: "root-ca",
	})
	if err != nil {
		return fmt.Errorf("failed to generate rootCa: %v", err)
	}

	log.Debugf("root CSR: %s", string(rootCerts.Csr))
	log.Debugf("root Cert: %s", string(rootCerts.Cert))
	log.Debugf("root Key: %s", string(rootCerts.Key))
	return nil
}
