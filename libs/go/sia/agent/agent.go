//
// Copyright The Athenz Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package agent

import (
	"bufio"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/AthenZ/athenz/clients/go/zts"
	"github.com/AthenZ/athenz/libs/go/sia/access/config"
	"github.com/AthenZ/athenz/libs/go/sia/access/tokens"
	"github.com/AthenZ/athenz/libs/go/sia/options"
	"github.com/AthenZ/athenz/libs/go/sia/sds"
	"github.com/AthenZ/athenz/libs/go/sia/ssh/hostkey"
	"github.com/AthenZ/athenz/libs/go/sia/util"
	"github.com/ardielle/ardielle-go/rdl"
	"github.com/cenkalti/backoff"
)

const siaMainDir = "/var/lib/sia"

func readCertificate(certFile string) (*x509.Certificate, error) {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return nil, err
	}
	var block *pem.Block
	block, _ = pem.Decode(data)
	if block == nil {
		return nil, nil
	}
	return x509.ParseCertificate(block.Bytes)
}

func GetPrevRoleCertDates(certFile string) (*rdl.Timestamp, *rdl.Timestamp, error) {
	prevRolCert, err := readCertificate(certFile)
	if err != nil {
		return nil, nil, err
	}

	notBefore := &rdl.Timestamp{
		Time: prevRolCert.NotBefore,
	}

	notAfter := &rdl.Timestamp{
		Time: prevRolCert.NotAfter,
	}

	log.Printf("Existing role cert %s, not before: %s, not after: %s\n", certFile, notBefore.String(), notAfter.String())
	return notBefore, notAfter, nil
}

func RoleKey(rotateKey bool, svcKey string) (*rsa.PrivateKey, error) {
	if rotateKey == true {
		return util.GenerateKeyPair(2048)
	}
	return util.PrivateKeyFromFile(svcKey)
}

func GetRoleCertificates(ztsUrl string, opts *options.Options) bool {

	//initialize our return state to success
	failures := 0

	var roleRequest = new(zts.RoleCertificateRequest)
	for _, role := range opts.Roles {

		svcKeyFile := util.GetSvcKeyFileName(opts.KeyDir, role.SvcKeyFilename, opts.Domain, role.Service)
		svcCertFile := util.GetSvcCertFileName(opts.CertDir, role.SvcCertFilename, opts.Domain, role.Service)

		client, err := util.ZtsClient(ztsUrl, opts.ZTSServerName, svcKeyFile, svcCertFile, opts.ZTSCACertFile)
		if err != nil {
			log.Printf("unable to initialize ZTS Client with url %s for role %s, err: %v\n", ztsUrl, role.Name, err)
			failures += 1
			continue
		}
		client.AddCredentials("User-Agent", opts.Version)

		key, err := util.PrivateKeyFromFile(svcKeyFile)
		if err != nil {
			log.Printf("unable to read private key from %s for role %s, err: %v\n", svcKeyFile, role.Name, err)
			failures += 1
			continue
		}

		if opts.GenerateRoleKey {
			var err error
			key, err = RoleKey(opts.RotateKey, svcKeyFile)
			if err != nil {
				log.Printf("unable to read generate/read key from %s, err: %v\n", role.Filename, err)
				failures += 1
				continue
			}
		}

		emailDomain := ""
		if opts.RolePrincipalEmail {
			emailDomain = opts.ZTSCloudDomains[0]
		}
		csr, err := util.GenerateRoleCertCSR(key, opts.CertCountryName, opts.CertOrgName, opts.Domain, role.Service, role.Name, opts.InstanceId, opts.Provider.GetName(), emailDomain)
		if err != nil {
			log.Printf("unable to generate CSR for %s, err: %v\n", role.Name, err)
			failures += 1
			continue
		}
		roleRequest.Csr = csr
		if role.ExpiryTime > 0 {
			roleRequest.ExpiryTime = int64(role.ExpiryTime)
		}

		certFilePem := util.GetRoleCertFileName(opts.CertDir, role.Filename, role.Name)
		notBefore, notAfter, _ := GetPrevRoleCertDates(certFilePem)
		roleRequest.PrevCertNotBefore = notBefore
		roleRequest.PrevCertNotAfter = notAfter
		if notBefore != nil && notAfter != nil {
			log.Printf("Previous Role Cert Not Before date: %s, Not After date: %s\n", notBefore, notAfter)
		}

		//"rolename": "athenz.fp:role.readers"
		//from the rolename, domain is athenz.fp
		//role is readers
		roleCert, err := client.PostRoleCertificateRequestExt(roleRequest)
		if err != nil {
			log.Printf("PostRoleCertificateRequest failed for %s, err: %v\n", role.Name, err)
			failures += 1
			continue
		}
		roleKeyBytes := util.PrivatePem(key)
		err = SaveRoleCertKey([]byte(roleKeyBytes), []byte(roleCert.X509Certificate), role, opts)
		if err != nil {
			log.Printf("Unable to save role cert key for role %s, err: %v\n", role.Name, err)
			failures += 1
			continue
		}
	}
	log.Printf("SIA processed %d (failures %d) role certificate requests\n", len(opts.Roles), failures)
	return failures == 0
}

func RegisterInstance(ztsUrl, metaEndpoint string, opts *options.Options, docExpiryCheck bool) error {

	//special handling for VM instances ( EC2 / GCE )
	//before we process our register event we need to check to
	//see if our timestamp in our document is less than 30 mins
	//ago otherwise ZTS server will reject the request and there
	//is no point of processing the request
	if docExpiryCheck && shouldSkipRegister(opts) {
		return fmt.Errorf("identity document has expired (30 min timeout). ZTS will not register this instance. Please relaunch or stop and start your instance to refesh its identity document")
	}

	for _, svc := range opts.Services {
		err := registerSvc(svc, ztsUrl, metaEndpoint, opts)
		if err != nil {
			return fmt.Errorf("unable to register identity for svc: %q, error: %v", svc.Name, err)
		}
	}
	return nil
}

func RefreshInstance(ztsUrl, metaEndpoint string, opts *options.Options) error {
	for _, svc := range opts.Services {
		err := refreshSvc(svc, ztsUrl, metaEndpoint, opts)
		if err != nil {
			return fmt.Errorf("unable to refresh identity for svc: %q, error: %v", svc.Name, err)
		}
	}
	return nil
}

func getServiceHostname(opts *options.Options, svc options.Service, fqdn bool) string {
	if !opts.SanDnsHostname {
		return ""
	}
	hostname := opts.Provider.GetHostname(fqdn)
	if hostname == "" {
		log.Println("No hostname configured for the instance")
		return ""
	}
	//if the hostname contains multiple components then we'll
	//return our hostname as is
	if strings.Contains(hostname, ".") {
		return hostname
	}
	//otherwise, we'll generate one based on the format
	//<hostname>.<service>.<domain>.<suffix> only if the
	//suffix is properly configured since we might be having
	//multiple suffix values
	if opts.HostnameSuffix == "" {
		// if our initial request was without fqdn then we're
		// going to retry with the fqdn otherwise we'll just
		// return an empty string
		if fqdn {
			log.Printf("No hostname suffix configured for the instance: %s\n", hostname)
			return ""
		} else {
			return getServiceHostname(opts, svc, true)
		}
	}

	hyphenDomain := strings.Replace(opts.Domain, ".", "-", -1)
	return fmt.Sprintf("%s.%s.%s.%s", hostname, svc.Name, hyphenDomain, opts.HostnameSuffix)
}

func registerSvc(svc options.Service, ztsUrl, metaEndpoint string, opts *options.Options) error {

	key, err := util.GenerateKeyPair(2048)
	if err != nil {
		return err
	}

	//if ssh support is enabled then we need to generate the csr
	//it is also generated for the primary service only
	hostname := getServiceHostname(opts, svc, false)
	sshCertRequest, sshCsr, err := generateSshRequest(opts, svc.Name, hostname)
	if err != nil {
		return err
	}

	//if the user hasn't configured to include the san dns hostname
	//then we're going to reset the hostname value to an empty string
	if !opts.SanDnsHostname {
		hostname = ""
	}
	csr, err := util.GenerateSvcCertCSR(key, opts.CertCountryName, opts.CertOrgName, opts.Domain, svc.Name, opts.Domain+"."+svc.Name, opts.InstanceId, opts.Provider.GetName(), hostname, opts.AddlSanDNSEntries, opts.ZTSCloudDomains, opts.SanDnsWildcard, opts.InstanceIdSanDNS)
	if err != nil {
		return err
	}
	attestData, err := opts.Provider.CloudAttestationData(metaEndpoint, svc.Name, ztsUrl)
	if err != nil {
		log.Printf("Failed to get attestation data to prove the identity, err:%v\n", err)
		return err
	}

	athenzJwk := true
	athenzJwkModified := util.GetAthenzJwkConfModTime(siaMainDir)

	info := &zts.InstanceRegisterInformation{
		Provider:          zts.ServiceName(opts.Provider.GetName()),
		Domain:            zts.DomainName(opts.Domain),
		Service:           zts.SimpleName(svc.Name),
		Csr:               csr,
		Ssh:               sshCsr,
		SshCertRequest:    sshCertRequest,
		AttestationData:   attestData,
		AthenzJWK:         &athenzJwk,
		AthenzJWKModified: &athenzJwkModified,
		Hostname:          zts.DomainName(hostname),
	}
	if svc.ExpiryTime > 0 {
		expiryTime := int32(svc.ExpiryTime)
		info.ExpiryTime = &expiryTime
	}

	client, err := util.ZtsClient(ztsUrl, opts.ZTSServerName, "", "", opts.ZTSCACertFile)
	if err != nil {
		return err
	}
	client.AddCredentials("User-Agent", opts.Version)
	ident, _, err := client.PostInstanceRegisterInformation(info)
	if err != nil {
		log.Printf("Unable to do PostInstanceRegisterInformation, err: %v\n", err)
		return err
	}
	svcKeyFile := util.GetSvcKeyFileName(opts.KeyDir, svc.KeyFilename, opts.Domain, svc.Name)
	err = util.UpdateFile(svcKeyFile, []byte(util.PrivatePem(key)), svc.Uid, svc.Gid, 0440, opts.FileDirectUpdate, true)
	if err != nil {
		return err
	}
	svcCertFile := util.GetSvcCertFileName(opts.CertDir, svc.CertFilename, opts.Domain, svc.Name)
	err = util.UpdateFile(svcCertFile, []byte(ident.X509Certificate), svc.Uid, svc.Gid, 0444, opts.FileDirectUpdate, true)
	if err != nil {
		return err
	}

	if opts.Services[0].Name == svc.Name {
		err = util.UpdateFile(opts.AthenzCACertFile, []byte(ident.X509CertificateSigner), svc.Uid, svc.Gid, 0444, opts.FileDirectUpdate, true)
		if err != nil {
			return err
		}
	}
	//we're not going to count ssh updates as fatal since the primary
	//task for sia to get service identity certs but we'll log the failure
	if ident.SshCertificate != "" {
		err = updateSSH(opts.SshCertFile, opts.SshConfigFile, ident.SshCertificate, opts.FileDirectUpdate)
		if err != nil {
			log.Printf("Unable to update ssh certificate, err: %v\n", err)
		}
	}

	if ident.AthenzJWK != nil {
		err = util.WriteAthenzJWKFile(ident.AthenzJWK, siaMainDir, svc.Uid, svc.Gid)
		if err != nil {
			return err
		}
	}
	return nil
}

func refreshSvc(svc options.Service, ztsUrl, metaEndpoint string, opts *options.Options) error {

	keyFile := util.GetSvcKeyFileName(opts.KeyDir, svc.KeyFilename, opts.Domain, svc.Name)
	certFile := util.GetSvcCertFileName(opts.CertDir, svc.CertFilename, opts.Domain, svc.Name)

	key, err := util.PrivateKey(keyFile, opts.RotateKey)
	if err != nil {
		log.Printf("Unable to read private key from %s, err: %v\n", keyFile, err)
		return err
	}

	//if ssh support is enabled then we need to generate the csr
	//it is also generated for the primary service only
	hostname := getServiceHostname(opts, svc, false)
	sshCertRequest, sshCsr, err := generateSshRequest(opts, svc.Name, hostname)
	if err != nil {
		return err
	}

	//if the user hasn't configured to include the san dns hostname
	//then we're going to reset the hostname value to an empty string
	if !opts.SanDnsHostname {
		hostname = ""
	}
	csr, err := util.GenerateSvcCertCSR(key, opts.CertCountryName, opts.CertOrgName, opts.Domain, svc.Name, opts.Domain+"."+svc.Name, opts.InstanceId, opts.Provider.GetName(), hostname, opts.AddlSanDNSEntries, opts.ZTSCloudDomains, opts.SanDnsWildcard, opts.InstanceIdSanDNS)
	if err != nil {
		log.Printf("Unable to generate CSR for %s, err: %v\n", opts.Name, err)
		return err
	}

	attestData, err := opts.Provider.CloudAttestationData(metaEndpoint, svc.Name, ztsUrl)
	if err != nil {
		log.Printf("Failed to get attestation data to prove the identity, err:%v\n", err)
		return err
	}

	athenzJwk := true
	athenzJwkModified := util.GetAthenzJwkConfModTime(siaMainDir)

	info := &zts.InstanceRefreshInformation{
		AttestationData:   attestData,
		Csr:               csr,
		Ssh:               sshCsr,
		SshCertRequest:    sshCertRequest,
		AthenzJWK:         &athenzJwk,
		AthenzJWKModified: &athenzJwkModified,
		Hostname:          zts.DomainName(hostname),
	}
	if svc.ExpiryTime > 0 {
		expiryTime := int32(svc.ExpiryTime)
		info.ExpiryTime = &expiryTime
	}

	client, err := util.ZtsClient(ztsUrl, opts.ZTSServerName, keyFile, certFile, opts.ZTSCACertFile)
	if err != nil {
		log.Printf("Unable to get ZTS Client for %s, err: %v\n", ztsUrl, err)
		return err
	}
	client.AddCredentials("User-Agent", opts.Version)

	ident, err := client.PostInstanceRefreshInformation(zts.ServiceName(opts.Provider.GetName()), zts.DomainName(opts.Domain), zts.SimpleName(svc.Name), zts.PathElement(opts.InstanceId), info)
	if err != nil {
		log.Printf("Unable to refresh instance service certificate for %s, err: %v\n", opts.Name, err)
		return err
	}

	svcKeyBytes := util.PrivatePem(key)
	svcCertBytes := []byte(ident.X509Certificate)
	prefix := fmt.Sprintf("%s.%s", opts.Domain, svc.Name)
	err = util.SaveServiceCertKey([]byte(svcKeyBytes), svcCertBytes, keyFile, certFile, prefix, svc.Uid, svc.Gid, svc.FileMode, opts.GenerateRoleKey, opts.RotateKey, opts.BackupDir, opts.FileDirectUpdate)
	if err != nil {
		return err
	}

	if opts.Services[0].Name == svc.Name {
		err = util.UpdateFile(opts.AthenzCACertFile, []byte(ident.X509CertificateSigner), svc.Uid, svc.Gid, 0444, opts.FileDirectUpdate, true)
		if err != nil {
			return err
		}
	}
	//we're not going to count ssh updates as fatal since the primary
	//task for sia to get service identity certs but we'll log the failure
	if ident.SshCertificate != "" {
		err = updateSSH(opts.SshCertFile, opts.SshConfigFile, ident.SshCertificate, opts.FileDirectUpdate)
		if err != nil {
			log.Printf("Unable to update ssh certificate, err: %v\n", err)
		}
	}

	if ident.AthenzJWK != nil {
		err = util.WriteAthenzJWKFile(ident.AthenzJWK, siaMainDir, svc.Uid, svc.Gid)
		if err != nil {
			return err
		}
	}
	return nil
}

func generateSshRequest(opts *options.Options, primaryServiceName, hostname string) (*zts.SSHCertRequest, string, error) {
	var err error
	var sshCsr string
	var sshCertRequest *zts.SSHCertRequest
	if opts.Ssh && opts.Services[0].Name == primaryServiceName {
		if opts.SshHostKeyType == hostkey.Rsa {
			sshCsr, err = util.GenerateSSHHostCSR(opts.SshPubKeyFile, opts.Domain, primaryServiceName, opts.PrivateIp, opts.ZTSCloudDomains)
		} else {
			sshCertRequest, err = util.GenerateSSHHostRequest(opts.SshPubKeyFile, opts.Domain, primaryServiceName, hostname, opts.PrivateIp, opts.InstanceId, opts.SshPrincipals, opts.ZTSAWSDomains)
		}
	}
	return sshCertRequest, sshCsr, err
}

func SaveRoleCertKey(key, cert []byte, role options.Role, opts *options.Options) error {
	certPrefix := role.Name
	if role.Filename != "" {
		certPrefix = strings.TrimSuffix(role.Filename, ".cert.pem")
	}
	keyPrefix := fmt.Sprintf("%s.%s", opts.Domain, role.Service)
	if opts.GenerateRoleKey == true {
		keyPrefix = role.Name
		if role.Filename != "" {
			keyPrefix = strings.TrimSuffix(role.Filename, ".cert.pem")
		}
	}
	return util.SaveRoleCertKey(key, cert, role.Filename, keyPrefix, certPrefix, role.Uid, role.Gid, role.FileMode, opts.GenerateRoleKey, opts.RotateKey, opts.KeyDir, opts.CertDir, opts.BackupDir, opts.FileDirectUpdate)
}

func restartSshdService() error {
	return exec.Command(util.GetUtilPath("systemctl"), "restart", "sshd").Run()
}

func updateSSH(sshCertFile, sshConfigFile, hostCert string, fileDirectUpdate bool) error {

	//write the host cert file
	err := util.UpdateFile(sshCertFile, []byte(hostCert), 0, 0, 0644, fileDirectUpdate, true)
	if err != nil {
		return err
	}

	//Now update the config file, if needed. The format of the line we're going
	//to insert is HostCertificate <sshCertFile>. so we'll see if the line exists
	//or not and if not we'll insert one at the end of the file
	if sshConfigFile != "" {
		configPresent, err := hostCertificateLinePresent(sshConfigFile, sshCertFile)
		if err != nil {
			log.Printf("unable to check host certificate line for %s - error %v\n", sshConfigFile, err)
			return err
		}
		if configPresent {
			return nil
		}
		//update the sshconfig file to include HostCertificate line
		err = updateSSHConfigFile(sshConfigFile, sshCertFile)
		if err != nil {
			return err
		}
		//and restart sshd to notice the changes.
		return restartSshdService()
	}
	return nil
}

func updateSSHConfigFile(sshConfigFile, sshCertFile string) error {
	//update the sshd config file to include HostCertificate line
	file, err := os.OpenFile(sshConfigFile, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	certLine := fmt.Sprintf("\nHostCertificate %s\n", sshCertFile)
	_, err = file.Write([]byte(certLine))
	if err != nil {
		return err
	}
	return nil
}

func hostCertificateLinePresent(sshConfigFile, sshCertFile string) (bool, error) {

	certLine := fmt.Sprintf("HostCertificate %s", sshCertFile)
	file, err := os.Open(sshConfigFile)
	if err != nil {
		return false, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := strings.Trim(scanner.Text(), " \t")
		if strings.HasPrefix(line, certLine) {
			log.Printf("ssh configuration file already includes expected line: %s\n", line)
			return true, nil
		}
	}
	return false, nil
}

func SetupAgent(opts *options.Options, siaMainDir, siaLinkDir string) {

	//first, let's determine if we need to drop our privileges
	//since it requires us to create the directories with the
	//specified ownership
	runUid, runGid := options.GetRunsAsUidGid(opts)

	//if our key/cert/token/backup directories are based on our sia main directory,
	//which indicates they haven't been configured explicitly, then we need to
	//create and setup up ownership
	if strings.HasPrefix(opts.KeyDir, siaMainDir) || strings.HasPrefix(opts.CertDir, siaMainDir) ||
		strings.HasPrefix(opts.TokenDir, siaMainDir) || strings.HasPrefix(opts.BackupDir, siaMainDir) {
		util.SetupSIADir(siaMainDir, runUid, runGid)
		//if we have a link directory specified then we'll create that as well
		if siaLinkDir != "" && !util.FileExists(siaLinkDir) {
			err := os.Symlink(siaMainDir, siaLinkDir)
			if err != nil {
				log.Printf("Unable to symlink SIA directory '%s': %v\n", siaLinkDir, err)
			}
		}
	}
	//make sure all component directories exist and have required ownership
	util.SetupSIADir(opts.KeyDir, runUid, runGid)
	util.SetupSIADir(opts.CertDir, runUid, runGid)
	util.SetupSIADir(opts.TokenDir, runUid, runGid)
	util.SetupSIADir(opts.BackupDir, runUid, runGid)

	//check to see if we need to drop our privileges and
	//run as the specific group id
	if runGid != -1 {
		if err := util.SyscallSetGid(runGid); err != nil {
			log.Printf("unable to drop privileges to group %d, error: %v\n", runGid, err)
		}
	}
	// same check for the user id
	if runUid != -1 {
		if err := util.SyscallSetUid(runUid); err != nil {
			log.Printf("unable to drop privileges to user %d, error: %v\n", runUid, err)
		}
	}
}

func RunAgent(siaCmd, ztsUrl, metaEndpoint string, opts *options.Options) {

	//the default value is to rotate once every day since our
	//server and role certs are valid for 30 days by default
	rotationInterval := time.Duration(opts.RefreshInterval) * time.Minute

	//data, err := opts.Provider.CloudAttestationData(opts)
	//if err != nil {
	//	log.Fatalf("Cannot determine identity to run as, err:%v\n", err)
	//}
	svcs := options.GetSvcNames(opts.Services)

	tokenOpts, err := tokenOptions(opts, ztsUrl)
	if err != nil {
		log.Printf(err.Error())
	}
	switch siaCmd {
	case "rolecert":
		GetRoleCertificates(ztsUrl, opts)
	case "token":
		if tokenOpts != nil {
			err := accessTokenRequest(tokenOpts)
			if err != nil {
				log.Fatalf("Unable to fetch access token, err: %v\n", err)
			}
		} else {
			log.Print("unable to obtain fetch token, invalid sia_config")
		}
	case "post", "register":
		err := RegisterInstance(ztsUrl, metaEndpoint, opts, false)
		if err != nil {
			log.Fatalf("Unable to register identity, err: %v\n", err)
		}
		log.Printf("identity registered for services: %s\n", svcs)
	case "rotate", "refresh":
		err = RefreshInstance(ztsUrl, metaEndpoint, opts)
		if err != nil {
			log.Fatalf("Refresh identity failed, err: %v\n", err)
		}
		log.Printf("Identity successfully refreshed for services: %s\n", svcs)
	default:
		// we're going to iterate through our configured services.
		// if the service key and certificate files exist then we're
		// going to refresh the identity, otherwise we're going to
		// register it. before registration, we'll verify that we
		// haven't passed our 30-min server enforced timeout since
		// there is no point to contact ZTS if it's going to reject it
		// for any refresh operations, we're going to skip any failures
		// since the existing file on disk is still valid, and we can
		// refresh during the next daily run.
		initialSetup := true
		for _, svc := range opts.Services {
			if serviceAlreadyRegistered(opts, svc) {
				err = refreshSvc(svc, ztsUrl, metaEndpoint, opts)
				if err != nil {
					log.Printf("unable to refresh identity for svc: %q, error: %v", svc.Name, err)
				}
			} else {
				if shouldSkipRegister(opts) {
					log.Fatalf("identity document has expired (30 min timeout). ZTS will not register this instance. Please relaunch or stop and start your instance to refesh its identity document")
				}
				err = registerSvc(svc, ztsUrl, metaEndpoint, opts)
				if err != nil {
					log.Fatalf("unable to register identity for svc: %q, error: %v", svc.Name, err)
				}
			}
		}

		log.Printf("Identity established for services: %s\n", svcs)

		stop := make(chan bool, 1)
		errors := make(chan error, 1)
		certUpdates := make(chan bool, 1)

		go func() {
			for {
				// if we just did our initial setup there is no point
				// to refresh the certs again. so we are going to skip
				// this time around and refresh certs next time

				if !initialSetup {
					err = RefreshInstance(ztsUrl, metaEndpoint, opts)
					if err != nil {
						errors <- fmt.Errorf("refresh identity failed: %v\n", err)
						return
					}
					log.Printf("identity successfully refreshed for services: %s\n", svcs)
				}
				initialSetup = false
				if tokenOpts != nil {
					err := accessTokenRequest(tokenOpts)
					if err != nil {
						errors <- fmt.Errorf("Unable to fetch access token after identity refresh, err: %v\n", err)
					}
				} else {
					log.Print("token config does not exist - do not refresh token")
				}
				GetRoleCertificates(ztsUrl, opts)
				if opts.SDSUdsPath != "" {
					certUpdates <- true
				}

				select {
				case <-stop:
					errors <- nil
					return
				case <-time.After(rotationInterval):
					break
				}
			}
		}()

		go func() {
			if opts.SDSUdsPath != "" {
				err := sds.StartGrpcServer(opts, certUpdates)
				if err != nil {
					log.Printf("failed to start grpc/uds server: %v\n", err)
					stop <- true
					return
				}
			}
		}()

		go func() {
			signals := make(chan os.Signal, 2)
			signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
			sig := <-signals
			log.Printf("Received signal %v, stopping rotation\n", sig)
			stop <- true
		}()

		go func() {
			if tokenOpts == nil || tokenOpts.TokenRefresh == 0 {
				return
			}

			log.Printf("start refresh access-token task every [%s]", fmt.Sprint(tokenOpts.TokenRefresh))
			t2 := time.NewTicker(tokenOpts.TokenRefresh)
			defer t2.Stop()
			for {
				select {
				case <-t2.C:
					log.Printf("refreshing access-token..")
					err := accessTokenRequest(tokenOpts)
					if err != nil {
						errors <- fmt.Errorf("refresh access-token task got error: %v\n", err)
					}
				case <-stop:
					errors <- nil
					return
				}
			}
		}()

		err = <-errors
		if err != nil {
			log.Printf("%v\n", err)
		}
	}
	os.Exit(0)
}

func accessTokenRequest(tokenOpts *config.TokenOptions) error {
	// getExponentialBackoffToken will return a backoff config with first retry delay of 5s, and backoff retry
	// until params.tokenRefresh / 4
	getExponentialBackoffToken := func() *backoff.ExponentialBackOff {
		b := backoff.NewExponentialBackOff()
		b.InitialInterval = 5 * time.Second
		b.Multiplier = 2
		b.MaxElapsedTime = tokenOpts.TokenRefresh / 4
		return b
	}

	notifyOnAccessTokenErr := func(err error, backoffDelay time.Duration) {
		log.Printf("Failed to create/refresh access token: %s. Retrying in %s", err.Error(), backoffDelay)
	}

	accessTokenFunc := func() error {
		return fetchAccessToken(tokenOpts)
	}
	err := backoff.RetryNotify(accessTokenFunc, getExponentialBackoffToken(), notifyOnAccessTokenErr)

	if err != nil {
		log.Printf("access tokens errors: %v", err)
	}
	return err
}

func tokenOptions(opts *options.Options, ztsUrl string) (*config.TokenOptions, error) {
	userAgent := fmt.Sprintf("%s-%s", opts.Provider, opts.InstanceId)
	tokenOpts, err := tokens.NewTokenOptions(options.LegacyOptions(opts), ztsUrl, userAgent)
	if err != nil {
		return nil, fmt.Errorf("unable to create token options: %s", err.Error())
	}
	tokenOpts.StoreOptions = config.AccessTokenProp

	log.Printf("token options created successfully")
	return tokenOpts, nil
}

func fetchAccessToken(tokenOpts *config.TokenOptions) error {

	_, errs := tokens.Fetch(tokenOpts)
	log.Printf("Fetch access token completed successfully with [%d] errors", len(errs))

	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		var errsStr []string
		for _, er := range errs {
			errsStr = append(errsStr, er.Error())
		}
		return fmt.Errorf(strings.Join(errsStr, ","))
	}
}

func shouldSkipRegister(opts *options.Options) bool {
	if opts.EC2StartTime == nil {
		return false
	}
	duration := time.Since(*opts.EC2StartTime)
	//our server timeout is 30 mins = 1800 secs
	return duration.Seconds() > 1800
}

func serviceAlreadyRegistered(opts *options.Options, svc options.Service) bool {
	keyFile := util.GetSvcKeyFileName(opts.KeyDir, svc.KeyFilename, opts.Domain, svc.Name)
	certFile := util.GetSvcCertFileName(opts.CertDir, svc.CertFilename, opts.Domain, svc.Name)
	return util.FileExists(keyFile) && util.FileExists(certFile)
}