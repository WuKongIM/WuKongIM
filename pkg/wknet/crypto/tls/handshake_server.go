// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tls

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"fmt"
	"hash"
	"io"
	"time"
)

/**

1. 客户端向服务器发送`ClientHello`消息，包括支持的TLS版本、加密套件、随机数等信息。
2. 服务器收到`ClientHello`消息后，解析其中的信息，并根据配置选择一个加密套件。
3. 服务器向客户端发送`ServerHello`消息，包括选择的TLS版本、加密套件、随机数等信息。
4. 服务器向客户端发送`Certificate`消息，包括服务器的证书和证书链。
5. 服务器向客户端发送`ServerKeyExchange`消息，包括服务器的公钥和密钥交换参数。
6. 服务器向客户端发送`ServerHelloDone`消息，表示握手过程的第一阶段已经完成。
7. 客户端收到`ServerHello`消息后，解析其中的信息，并根据配置选择一个加密套件。
8. 客户端向服务器发送`ClientKeyExchange`消息，包括客户端的公钥和密钥交换参数。
9. 客户端向服务器发送`ChangeCipherSpec`消息，表示客户端已经准备好使用新的加密套件。
10. 客户端向服务器发送`Finished`消息，包括握手过程的所有消息的哈希值。
11. 服务器收到`ClientKeyExchange`消息后，解析其中的信息，并使用客户端的公钥和密钥交换参数计算出共享密钥。
12. 服务器收到`ChangeCipherSpec`消息后，表示客户端已经准备好使用新的加密套件，服务器也准备好使用新的加密套件。
13. 服务器向客户端发送`Finished`消息，包括握手过程的所有消息的哈希值。
14. 握手过程完成，客户端和服务器开始使用共享密钥进行加密通信。
**/

// serverHandshakeState contains details of a server handshake in progress.
// It's discarded once the handshake has completed.
type serverHandshakeState struct {
	c            *Conn
	ctx          context.Context
	clientHello  *clientHelloMsg
	hello        *serverHelloMsg
	suite        *cipherSuite
	ecdheOk      bool
	ecSignOk     bool
	rsaDecryptOk bool
	rsaSignOk    bool
	sessionState *sessionState
	finishedHash finishedHash
	masterSecret []byte
	cert         *Certificate

	// 魔改
	err                  error
	checkForResumptionOk bool
	kgm                  keyAgreement
}

// serverHandshake performs a TLS handshake as a server.
func (c *Conn) serverHandshake(ctx context.Context) error {
	var err error
	if c.handshakeStatus < handshakeStatusReadClientHello {
		c.clientHello, err = c.readClientHello(ctx)
		if err != nil {
			return err
		}
		c.handshakeStatus = handshakeStatusReadClientHello
	}

	if c.vers == VersionTLS13 {
		hs := c.hs13
		if hs == nil {
			hs = &serverHandshakeStateTLS13{
				c:           c,
				ctx:         ctx,
				clientHello: c.clientHello,
			}
			c.hs13 = hs
		}
		return hs.handshake()
	}

	if c.hs == nil {
		c.hs = &serverHandshakeState{
			c:           c,
			ctx:         ctx,
			clientHello: c.clientHello,
		}
	}

	return c.hs.handshake()
}

func (hs *serverHandshakeState) handshake() error {
	c := hs.c

	c.buffering = false

	if c.handshakeStatus >= handshakeStatusHandshakeDone {
		return nil
	}

	if hs.err != nil && hs.err != ErrDataNotEnough {
		return hs.err
	}

	if err := hs.processClientHello(); err != nil {
		hs.err = err
		return err
	}

	// For an overview of TLS handshaking, see RFC 5246, Section 7.3.
	c.buffering = true
	if hs.checkForResumption() {
		// The client has included a session ticket and so we do an abbreviated handshake.
		c.didResume = true
		if err := hs.doResumeHandshake(); err != nil {
			return err
		}
		if err := hs.establishKeys(); err != nil {
			return err
		}
		if err := hs.sendSessionTicket(); err != nil {
			return err
		}
		if err := hs.sendFinished(c.serverFinished[:]); err != nil {
			return err
		}
		if _, err := c.flush(); err != nil {
			return err
		}
		c.clientFinishedIsFirst = false
		if err := hs.readFinished(nil); err != nil {
			return err
		}
	} else {
		// The client didn't include a session ticket, or it wasn't
		// valid so we do a full handshake.
		if err := hs.pickCipherSuite(); err != nil {
			return err
		}
		if err := hs.doFullHandshake(); err != nil {
			return err
		}
		if err := hs.establishKeys(); err != nil {
			return err
		}
		if err := hs.readFinished(c.clientFinished[:]); err != nil {
			return err
		}
		c.clientFinishedIsFirst = true
		c.buffering = true
		if err := hs.sendSessionTicket2(); err != nil {
			return err
		}
		if err := hs.sendFinished2(nil); err != nil {
			return err
		}
		if _, err := c.flush(); err != nil {
			return err
		}
	}

	c.ekm = ekmFromMasterSecret(c.vers, hs.suite, hs.masterSecret, hs.clientHello.random, hs.hello.random)
	c.isHandshakeComplete.Store(true)

	return nil
}

// readClientHello reads a ClientHello message and selects the protocol version.
func (c *Conn) readClientHello(ctx context.Context) (*clientHelloMsg, error) {
	// clientHelloMsg is included in the transcript, but we haven't initialized
	// it yet. The respective handshake functions will record it themselves.
	msg, err := c.readHandshake(nil)
	if err != nil {
		return nil, err
	}
	clientHello, ok := msg.(*clientHelloMsg)
	if !ok {
		c.sendAlert(alertUnexpectedMessage)
		return nil, unexpectedMessageError(clientHello, msg)
	}

	var configForClient *Config
	originalConfig := c.config
	if c.config.GetConfigForClient != nil {
		chi := clientHelloInfo(ctx, c, clientHello)
		if configForClient, err = c.config.GetConfigForClient(chi); err != nil {
			c.sendAlert(alertInternalError)
			return nil, err
		} else if configForClient != nil {
			c.config = configForClient
		}
	}
	c.ticketKeys = originalConfig.ticketKeys(configForClient)

	clientVersions := clientHello.supportedVersions
	if len(clientHello.supportedVersions) == 0 {
		clientVersions = supportedVersionsFromMax(clientHello.vers)
	}
	c.vers, ok = c.config.mutualVersion(roleServer, clientVersions)
	if !ok {
		c.sendAlert(alertProtocolVersion)
		return nil, fmt.Errorf("tls: client offered only unsupported versions: %x", clientVersions)
	}
	c.haveVers = true
	c.in.version = c.vers
	c.out.version = c.vers

	return clientHello, nil
}

func (hs *serverHandshakeState) processClientHello() error {
	c := hs.c

	if c.handshakeStatus >= handshakeStatusProcessClientHello {
		return nil
	}
	c.handshakeStatus = handshakeStatusProcessClientHello

	hs.hello = new(serverHelloMsg)
	hs.hello.vers = c.vers

	foundCompression := false
	// We only support null compression, so check that the client offered it.
	for _, compression := range hs.clientHello.compressionMethods {
		if compression == compressionNone {
			foundCompression = true
			break
		}
	}

	if !foundCompression {
		c.sendAlert(alertHandshakeFailure)
		return errors.New("tls: client does not support uncompressed connections")
	}

	hs.hello.random = make([]byte, 32)
	serverRandom := hs.hello.random
	// Downgrade protection canaries. See RFC 8446, Section 4.1.3.
	maxVers := c.config.maxSupportedVersion(roleServer)
	if maxVers >= VersionTLS12 && c.vers < maxVers || testingOnlyForceDowngradeCanary {
		if c.vers == VersionTLS12 {
			copy(serverRandom[24:], downgradeCanaryTLS12)
		} else {
			copy(serverRandom[24:], downgradeCanaryTLS11)
		}
		serverRandom = serverRandom[:24]
	}
	_, err := io.ReadFull(c.config.rand(), serverRandom)
	if err != nil {
		c.sendAlert(alertInternalError)
		return err
	}

	if len(hs.clientHello.secureRenegotiation) != 0 {
		c.sendAlert(alertHandshakeFailure)
		return errors.New("tls: initial handshake had non-empty renegotiation extension")
	}

	hs.hello.secureRenegotiationSupported = hs.clientHello.secureRenegotiationSupported
	hs.hello.compressionMethod = compressionNone
	if len(hs.clientHello.serverName) > 0 {
		c.serverName = hs.clientHello.serverName
	}

	selectedProto, err := negotiateALPN(c.config.NextProtos, hs.clientHello.alpnProtocols)
	if err != nil {
		c.sendAlert(alertNoApplicationProtocol)
		return err
	}
	hs.hello.alpnProtocol = selectedProto
	c.clientProtocol = selectedProto

	hs.cert, err = c.config.getCertificate(clientHelloInfo(hs.ctx, c, hs.clientHello))
	if err != nil {
		if err == errNoCertificates {
			c.sendAlert(alertUnrecognizedName)
		} else {
			c.sendAlert(alertInternalError)
		}
		return err
	}
	if hs.clientHello.scts {
		hs.hello.scts = hs.cert.SignedCertificateTimestamps
	}

	hs.ecdheOk = supportsECDHE(c.config, hs.clientHello.supportedCurves, hs.clientHello.supportedPoints)

	if hs.ecdheOk && len(hs.clientHello.supportedPoints) > 0 {
		// Although omitting the ec_point_formats extension is permitted, some
		// old OpenSSL version will refuse to handshake if not present.
		//
		// Per RFC 4492, section 5.1.2, implementations MUST support the
		// uncompressed point format. See golang.org/issue/31943.
		hs.hello.supportedPoints = []uint8{pointFormatUncompressed}
	}

	if priv, ok := hs.cert.PrivateKey.(crypto.Signer); ok {
		switch priv.Public().(type) {
		case *ecdsa.PublicKey:
			hs.ecSignOk = true
		case ed25519.PublicKey:
			hs.ecSignOk = true
		case *rsa.PublicKey:
			hs.rsaSignOk = true
		default:
			c.sendAlert(alertInternalError)
			return fmt.Errorf("tls: unsupported signing key type (%T)", priv.Public())
		}
	}
	if priv, ok := hs.cert.PrivateKey.(crypto.Decrypter); ok {
		switch priv.Public().(type) {
		case *rsa.PublicKey:
			hs.rsaDecryptOk = true
		default:
			c.sendAlert(alertInternalError)
			return fmt.Errorf("tls: unsupported decryption key type (%T)", priv.Public())
		}
	}

	return nil
}

// negotiateALPN picks a shared ALPN protocol that both sides support in server
// preference order. If ALPN is not configured or the peer doesn't support it,
// it returns "" and no error.
func negotiateALPN(serverProtos, clientProtos []string) (string, error) {
	if len(serverProtos) == 0 || len(clientProtos) == 0 {
		return "", nil
	}
	var http11fallback bool
	for _, s := range serverProtos {
		for _, c := range clientProtos {
			if s == c {
				return s, nil
			}
			if s == "h2" && c == "http/1.1" {
				http11fallback = true
			}
		}
	}
	// As a special case, let http/1.1 clients connect to h2 servers as if they
	// didn't support ALPN. We used not to enforce protocol overlap, so over
	// time a number of HTTP servers were configured with only "h2", but
	// expected to accept connections from "http/1.1" clients. See Issue 46310.
	if http11fallback {
		return "", nil
	}
	return "", fmt.Errorf("tls: client requested unsupported application protocols (%s)", clientProtos)
}

// supportsECDHE returns whether ECDHE key exchanges can be used with this
// pre-TLS 1.3 client.
func supportsECDHE(c *Config, supportedCurves []CurveID, supportedPoints []uint8) bool {
	supportsCurve := false
	for _, curve := range supportedCurves {
		if c.supportsCurve(curve) {
			supportsCurve = true
			break
		}
	}

	supportsPointFormat := false
	for _, pointFormat := range supportedPoints {
		if pointFormat == pointFormatUncompressed {
			supportsPointFormat = true
			break
		}
	}
	// Per RFC 8422, Section 5.1.2, if the Supported Point Formats extension is
	// missing, uncompressed points are supported. If supportedPoints is empty,
	// the extension must be missing, as an empty extension body is rejected by
	// the parser. See https://go.dev/issue/49126.
	if len(supportedPoints) == 0 {
		supportsPointFormat = true
	}

	return supportsCurve && supportsPointFormat
}

func (hs *serverHandshakeState) pickCipherSuite() error {
	c := hs.c

	if c.handshakeStatus >= handshakeStatusPickCipherSuite2 {
		return nil
	}
	c.handshakeStatus = handshakeStatusPickCipherSuite2

	preferenceOrder := cipherSuitesPreferenceOrder
	if !hasAESGCMHardwareSupport || !aesgcmPreferred(hs.clientHello.cipherSuites) {
		preferenceOrder = cipherSuitesPreferenceOrderNoAES
	}

	configCipherSuites := c.config.cipherSuites()
	preferenceList := make([]uint16, 0, len(configCipherSuites))
	for _, suiteID := range preferenceOrder {
		for _, id := range configCipherSuites {
			if id == suiteID {
				preferenceList = append(preferenceList, id)
				break
			}
		}
	}

	hs.suite = selectCipherSuite(preferenceList, hs.clientHello.cipherSuites, hs.cipherSuiteOk)
	if hs.suite == nil {
		c.sendAlert(alertHandshakeFailure)
		return errors.New("tls: no cipher suite supported by both client and server")
	}
	c.cipherSuite = hs.suite.id

	for _, id := range hs.clientHello.cipherSuites {
		if id == TLS_FALLBACK_SCSV {
			// The client is doing a fallback connection. See RFC 7507.
			if hs.clientHello.vers < c.config.maxSupportedVersion(roleServer) {
				c.sendAlert(alertInappropriateFallback)
				return errors.New("tls: client using inappropriate protocol fallback")
			}
			break
		}
	}

	return nil
}

func (hs *serverHandshakeState) cipherSuiteOk(c *cipherSuite) bool {
	if c.flags&suiteECDHE != 0 {
		if !hs.ecdheOk {
			return false
		}
		if c.flags&suiteECSign != 0 {
			if !hs.ecSignOk {
				return false
			}
		} else if !hs.rsaSignOk {
			return false
		}
	} else if !hs.rsaDecryptOk {
		return false
	}
	if hs.c.vers < VersionTLS12 && c.flags&suiteTLS12 != 0 {
		return false
	}
	return true
}

// checkForResumption reports whether we should perform resumption on this connection.
func (hs *serverHandshakeState) checkForResumption() bool {
	c := hs.c

	if c.handshakeStatus >= handshakeStatusCheckForResumption {
		return hs.checkForResumptionOk
	}
	c.handshakeStatus = handshakeStatusCheckForResumption

	if c.config.SessionTicketsDisabled {
		hs.checkForResumptionOk = false
		return false
	}

	plaintext, usedOldKey := c.decryptTicket(hs.clientHello.sessionTicket)
	if plaintext == nil {
		hs.checkForResumptionOk = false
		return false
	}
	hs.sessionState = &sessionState{usedOldKey: usedOldKey}
	ok := hs.sessionState.unmarshal(plaintext)
	if !ok {
		hs.checkForResumptionOk = false
		return false
	}

	createdAt := time.Unix(int64(hs.sessionState.createdAt), 0)
	if c.config.time().Sub(createdAt) > maxSessionTicketLifetime {
		hs.checkForResumptionOk = false
		return false
	}

	// Never resume a session for a different TLS version.
	if c.vers != hs.sessionState.vers {
		hs.checkForResumptionOk = false
		return false
	}

	cipherSuiteOk := false
	// Check that the client is still offering the ciphersuite in the session.
	for _, id := range hs.clientHello.cipherSuites {
		if id == hs.sessionState.cipherSuite {
			cipherSuiteOk = true
			break
		}
	}
	if !cipherSuiteOk {
		hs.checkForResumptionOk = false
		return false
	}

	// Check that we also support the ciphersuite from the session.
	hs.suite = selectCipherSuite([]uint16{hs.sessionState.cipherSuite},
		c.config.cipherSuites(), hs.cipherSuiteOk)
	if hs.suite == nil {
		hs.checkForResumptionOk = false
		return false
	}

	sessionHasClientCerts := len(hs.sessionState.certificates) != 0
	needClientCerts := requiresClientCert(c.config.ClientAuth)
	if needClientCerts && !sessionHasClientCerts {
		hs.checkForResumptionOk = false
		return false
	}
	if sessionHasClientCerts && c.config.ClientAuth == NoClientCert {
		hs.checkForResumptionOk = false
		return false
	}
	hs.checkForResumptionOk = true
	return true
}

func (hs *serverHandshakeState) doResumeHandshake() error {
	c := hs.c

	if c.handshakeStatus >= handshakeStatusDoResumeHandshake {
		return nil
	}
	c.handshakeStatus = handshakeStatusDoResumeHandshake

	hs.hello.cipherSuite = hs.suite.id
	c.cipherSuite = hs.suite.id
	// We echo the client's session ID in the ServerHello to let it know
	// that we're doing a resumption.
	hs.hello.sessionId = hs.clientHello.sessionId
	hs.hello.ticketSupported = hs.sessionState.usedOldKey
	hs.finishedHash = newFinishedHash(c.vers, hs.suite)
	hs.finishedHash.discardHandshakeBuffer()
	if err := transcriptMsg(hs.clientHello, &hs.finishedHash); err != nil {
		return err
	}
	if _, err := hs.c.writeHandshakeRecord(hs.hello, &hs.finishedHash); err != nil {
		return err
	}

	if err := c.processCertsFromClient(Certificate{
		Certificate: hs.sessionState.certificates,
	}); err != nil {
		return err
	}

	if c.config.VerifyConnection != nil {
		if err := c.config.VerifyConnection(c.connectionStateLocked()); err != nil {
			c.sendAlert(alertBadCertificate)
			return err
		}
	}

	hs.masterSecret = hs.sessionState.masterSecret

	return nil
}

func (hs *serverHandshakeState) doFullHandshake() error {
	c := hs.c
	var certReq *certificateRequestMsg

	initCertReq := func() {
		if certReq != nil {
			return
		}
		// Request a client certificate
		certReq = new(certificateRequestMsg)
		certReq.certificateTypes = []byte{
			byte(certTypeRSASign),
			byte(certTypeECDSASign),
		}
		if c.vers >= VersionTLS12 {
			certReq.hasSignatureAlgorithm = true
			certReq.supportedSignatureAlgorithms = supportedSignatureAlgorithms()
		}
		if c.config.ClientCAs != nil {
			certReq.certificateAuthorities = c.config.ClientCAs.Subjects()
		}
	}

	if c.handshakeStatus < handshakeStatusDoFullHandshake2 {
		c.handshakeStatus = handshakeStatusDoFullHandshake2
		// #################### 服务器向客户端发送ServerHello消息，包括选择的TLS版本、加密套件、随机数等信息。 ####################
		if hs.clientHello.ocspStapling && len(hs.cert.OCSPStaple) > 0 {
			hs.hello.ocspStapling = true
		}

		hs.hello.ticketSupported = hs.clientHello.ticketSupported && !c.config.SessionTicketsDisabled
		hs.hello.cipherSuite = hs.suite.id

		hs.finishedHash = newFinishedHash(hs.c.vers, hs.suite)
		if c.config.ClientAuth == NoClientCert {
			// No need to keep a full record of the handshake if client
			// certificates won't be used.
			hs.finishedHash.discardHandshakeBuffer()
		}
		if err := transcriptMsg(hs.clientHello, &hs.finishedHash); err != nil {
			return err
		}
		if _, err := hs.c.writeHandshakeRecord(hs.hello, &hs.finishedHash); err != nil {
			return err
		}

		// #################### 服务器向客户端发送Certificate消息，包括服务器的证书和证书链。 ####################
		certMsg := new(certificateMsg)
		certMsg.certificates = hs.cert.Certificate
		if _, err := hs.c.writeHandshakeRecord(certMsg, &hs.finishedHash); err != nil {
			return err
		}

		if hs.hello.ocspStapling {
			certStatus := new(certificateStatusMsg)
			certStatus.response = hs.cert.OCSPStaple
			if _, err := hs.c.writeHandshakeRecord(certStatus, &hs.finishedHash); err != nil {
				return err
			}
		}
		// #################### 服务器向客户端发送ServerKeyExchange消息，包括服务器的公钥和密钥交换参数 ####################
		hs.kgm = hs.suite.ka(c.vers)
		skx, err := hs.kgm.generateServerKeyExchange(c.config, hs.cert, hs.clientHello, hs.hello)
		if err != nil {
			c.sendAlert(alertHandshakeFailure)
			return err
		}
		if skx != nil {
			if _, err := hs.c.writeHandshakeRecord(skx, &hs.finishedHash); err != nil {
				return err
			}
		}
		if c.config.ClientAuth >= RequestClientCert {
			initCertReq()
			if _, err := hs.c.writeHandshakeRecord(certReq, &hs.finishedHash); err != nil {
				return err
			}
		}
		// #################### 服务器向客户端发送ServerHelloDone消息，表示握手过程的第一阶段已经完成 ####################
		helloDone := new(serverHelloDoneMsg)
		if _, err := hs.c.writeHandshakeRecord(helloDone, &hs.finishedHash); err != nil {
			return err
		}

		if _, err := c.flush(); err != nil {
			return err
		}
	}

	var pub crypto.PublicKey // public key for client auth, if any

	// #################### 客户端向服务器发送ClientKeyExchange消息，包括客户端的公钥和密钥交换参数 ####################
	var msg any
	var err error
	if c.handshakeStatus < handshakeStatusDoFullHandshake2ReadHandshake1 {
		msg, err = c.readHandshake(&hs.finishedHash)
		if err != nil {
			if err != ErrDataNotEnough {
				c.handshakeStatus = handshakeStatusDoFullHandshake2ReadHandshake1
			}
			return err
		}
		c.handshakeStatus = handshakeStatusDoFullHandshake2ReadHandshake1
	}
	// If we requested a client certificate, then the client must send a
	// certificate message, even if it's empty.
	if c.config.ClientAuth >= RequestClientCert {
		if c.handshakeStatus < handshakeStatusDoFullHandshake2HandleCertificateMsg {
			c.handshakeStatus = handshakeStatusDoFullHandshake2HandleCertificateMsg

			certMsg, ok := msg.(*certificateMsg)
			if !ok {
				c.sendAlert(alertUnexpectedMessage)
				return unexpectedMessageError(certMsg, msg)
			}

			if err := c.processCertsFromClient(Certificate{
				Certificate: certMsg.certificates,
			}); err != nil {
				return err
			}
			if len(certMsg.certificates) != 0 {
				pub = c.peerCertificates[0].PublicKey
			}
		}

		// #################### 客户端向服务器发送ClientKeyExchange消息，包括客户端的公钥和密钥交换参数。 ####################
		if c.handshakeStatus < handshakeStatusDoFullHandshake2ReadHandshake2 {
			msg, err = c.readHandshake(&hs.finishedHash)
			if err != nil {
				if err != ErrDataNotEnough {
					c.handshakeStatus = handshakeStatusDoFullHandshake2ReadHandshake2
				}
				return err
			}
			c.handshakeStatus = handshakeStatusDoFullHandshake2ReadHandshake2
		}
	}

	// #################### VerifyConnection ####################
	if c.handshakeStatus < handshakeStatusDoFullHandshake2HandleVerifyConnection {
		c.handshakeStatus = handshakeStatusDoFullHandshake2HandleVerifyConnection
		if c.config.VerifyConnection != nil {
			if err := c.config.VerifyConnection(c.connectionStateLocked()); err != nil {
				c.sendAlert(alertBadCertificate)
				return err
			}
		}
		// Get client key exchange
		ckx, ok := msg.(*clientKeyExchangeMsg)
		if !ok {
			c.sendAlert(alertUnexpectedMessage)
			return unexpectedMessageError(ckx, msg)
		}

		preMasterSecret, err := hs.kgm.processClientKeyExchange(c.config, hs.cert, ckx, c.vers)
		if err != nil {
			c.sendAlert(alertHandshakeFailure)
			return err
		}
		hs.masterSecret = masterFromPreMasterSecret(c.vers, hs.suite, preMasterSecret, hs.clientHello.random, hs.hello.random)
		if err := c.config.writeKeyLog(keyLogLabelTLS12, hs.clientHello.random, hs.masterSecret); err != nil {
			c.sendAlert(alertInternalError)
			return err
		}
	}

	if c.handshakeStatus >= handshakeStatusDoFullHandshake2ReadHandshake3 {
		return nil
	}

	// #################### 客户端向服务器发送ChangeCipherSpec消息，表示客户端已经准备好使用新的加密套件。 ####################
	// If we received a client cert in response to our certificate request message,
	// the client will send us a certificateVerifyMsg immediately after the
	// clientKeyExchangeMsg. This message is a digest of all preceding
	// handshake-layer messages that is signed using the private key corresponding
	// to the client's certificate. This allows us to verify that the client is in
	// possession of the private key of the certificate.

	if len(c.peerCertificates) > 0 {
		// certificateVerifyMsg is included in the transcript, but not until
		// after we verify the handshake signature, since the state before
		// this message was sent is used.
		msg, err = c.readHandshake(nil)
		if err != nil {
			if err != ErrDataNotEnough {
				c.handshakeStatus = handshakeStatusDoFullHandshake2ReadHandshake3
			}
			return err
		}
		certVerify, ok := msg.(*certificateVerifyMsg)
		if !ok {
			c.sendAlert(alertUnexpectedMessage)
			c.handshakeStatus = handshakeStatusDoFullHandshake2ReadHandshake3
			return unexpectedMessageError(certVerify, msg)
		}

		var sigType uint8
		var sigHash crypto.Hash
		if c.vers >= VersionTLS12 {
			initCertReq()
			if !isSupportedSignatureAlgorithm(certVerify.signatureAlgorithm, certReq.supportedSignatureAlgorithms) {
				c.sendAlert(alertIllegalParameter)
				c.handshakeStatus = handshakeStatusDoFullHandshake2ReadHandshake3
				return errors.New("tls: client certificate used with invalid signature algorithm")
			}
			sigType, sigHash, err = typeAndHashFromSignatureScheme(certVerify.signatureAlgorithm)
			if err != nil {
				c.handshakeStatus = handshakeStatusDoFullHandshake2ReadHandshake3
				return c.sendAlert(alertInternalError)
			}
		} else {
			sigType, sigHash, err = legacyTypeAndHashFromPublicKey(pub)
			if err != nil {
				c.handshakeStatus = handshakeStatusDoFullHandshake2ReadHandshake3
				c.sendAlert(alertIllegalParameter)
				return err
			}
		}

		signed := hs.finishedHash.hashForClientCertificate(sigType, sigHash)
		if err := verifyHandshakeSignature(sigType, pub, sigHash, signed, certVerify.signature); err != nil {
			c.sendAlert(alertDecryptError)
			c.handshakeStatus = handshakeStatusDoFullHandshake2ReadHandshake3
			return errors.New("tls: invalid signature by the client certificate: " + err.Error())
		}

		if err := transcriptMsg(certVerify, &hs.finishedHash); err != nil {
			c.handshakeStatus = handshakeStatusDoFullHandshake2ReadHandshake3
			return err
		}
	}

	hs.finishedHash.discardHandshakeBuffer()
	c.handshakeStatus = handshakeStatusDoFullHandshake2ReadHandshake3
	return nil
}

func (hs *serverHandshakeState) establishKeys() error {
	c := hs.c

	if c.handshakeStatus >= handshakeStatusHandshakeEstablishKeys2 {
		return nil
	}
	c.handshakeStatus = handshakeStatusHandshakeEstablishKeys2

	clientMAC, serverMAC, clientKey, serverKey, clientIV, serverIV :=
		keysFromMasterSecret(c.vers, hs.suite, hs.masterSecret, hs.clientHello.random, hs.hello.random, hs.suite.macLen, hs.suite.keyLen, hs.suite.ivLen)

	var clientCipher, serverCipher any
	var clientHash, serverHash hash.Hash

	if hs.suite.aead == nil {
		clientCipher = hs.suite.cipher(clientKey, clientIV, true /* for reading */)
		clientHash = hs.suite.mac(clientMAC)
		serverCipher = hs.suite.cipher(serverKey, serverIV, false /* not for reading */)
		serverHash = hs.suite.mac(serverMAC)
	} else {
		clientCipher = hs.suite.aead(clientKey, clientIV)
		serverCipher = hs.suite.aead(serverKey, serverIV)
	}

	c.in.prepareCipherSpec(c.vers, clientCipher, clientHash)
	c.out.prepareCipherSpec(c.vers, serverCipher, serverHash)

	return nil
}

func (hs *serverHandshakeState) readFinished(out []byte) error {
	c := hs.c

	if c.handshakeStatus < handshakeStatusReadFinishedReadChangeCipherSpec {
		if err := c.readChangeCipherSpec(); err != nil {
			if err != ErrDataNotEnough {
				c.handshakeStatus = handshakeStatusReadFinishedReadChangeCipherSpec
			}
			return err
		}
		c.handshakeStatus = handshakeStatusReadFinishedReadChangeCipherSpec
	}
	if c.handshakeStatus >= handshakeStatusReadFinishedDone {
		return nil
	}

	// finishedMsg is included in the transcript, but not until after we
	// check the client version, since the state before this message was
	// sent is used during verification.
	msg, err := c.readHandshake(nil)
	if err != nil {
		if err != ErrDataNotEnough {
			c.handshakeStatus = handshakeStatusReadFinishedDone
		}
		return err
	}
	clientFinished, ok := msg.(*finishedMsg)
	if !ok {
		c.handshakeStatus = handshakeStatusReadFinishedDone
		c.sendAlert(alertUnexpectedMessage)
		return unexpectedMessageError(clientFinished, msg)
	}

	verify := hs.finishedHash.clientSum(hs.masterSecret)
	if len(verify) != len(clientFinished.verifyData) ||
		subtle.ConstantTimeCompare(verify, clientFinished.verifyData) != 1 {
		c.sendAlert(alertHandshakeFailure)
		c.handshakeStatus = handshakeStatusReadFinishedDone
		return errors.New("tls: client's Finished message is incorrect")
	}

	if err := transcriptMsg(clientFinished, &hs.finishedHash); err != nil {
		c.handshakeStatus = handshakeStatusReadFinishedDone
		return err
	}

	copy(out, verify)

	c.handshakeStatus = handshakeStatusReadFinishedDone
	return nil
}

func (hs *serverHandshakeState) sendSessionTicket() error {
	// ticketSupported is set in a resumption handshake if the
	// ticket from the client was encrypted with an old session
	// ticket key and thus a refreshed ticket should be sent.
	if !hs.hello.ticketSupported {
		return nil
	}

	c := hs.c

	if c.handshakeStatus >= handshakeStatusSendSessionTicket {
		return nil
	}
	c.handshakeStatus = handshakeStatusSendSessionTicket

	m := new(newSessionTicketMsg)

	createdAt := uint64(c.config.time().Unix())
	if hs.sessionState != nil {
		// If this is re-wrapping an old key, then keep
		// the original time it was created.
		createdAt = hs.sessionState.createdAt
	}

	var certsFromClient [][]byte
	for _, cert := range c.peerCertificates {
		certsFromClient = append(certsFromClient, cert.Raw)
	}
	state := sessionState{
		vers:         c.vers,
		cipherSuite:  hs.suite.id,
		createdAt:    createdAt,
		masterSecret: hs.masterSecret,
		certificates: certsFromClient,
	}
	stateBytes, err := state.marshal()
	if err != nil {
		return err
	}
	m.ticket, err = c.encryptTicket(stateBytes)
	if err != nil {
		return err
	}

	if _, err := hs.c.writeHandshakeRecord(m, &hs.finishedHash); err != nil {
		return err
	}

	return nil
}

func (hs *serverHandshakeState) sendSessionTicket2() error {
	// ticketSupported is set in a resumption handshake if the
	// ticket from the client was encrypted with an old session
	// ticket key and thus a refreshed ticket should be sent.
	if !hs.hello.ticketSupported {
		return nil
	}

	c := hs.c

	if c.handshakeStatus >= handshakeStatusSendSessionTicket2 {
		return nil
	}
	c.handshakeStatus = handshakeStatusSendSessionTicket2

	m := new(newSessionTicketMsg)

	createdAt := uint64(c.config.time().Unix())
	if hs.sessionState != nil {
		// If this is re-wrapping an old key, then keep
		// the original time it was created.
		createdAt = hs.sessionState.createdAt
	}

	var certsFromClient [][]byte
	for _, cert := range c.peerCertificates {
		certsFromClient = append(certsFromClient, cert.Raw)
	}
	state := sessionState{
		vers:         c.vers,
		cipherSuite:  hs.suite.id,
		createdAt:    createdAt,
		masterSecret: hs.masterSecret,
		certificates: certsFromClient,
	}
	stateBytes, err := state.marshal()
	if err != nil {
		return err
	}
	m.ticket, err = c.encryptTicket(stateBytes)
	if err != nil {
		return err
	}

	if _, err := hs.c.writeHandshakeRecord(m, &hs.finishedHash); err != nil {
		return err
	}

	return nil
}

func (hs *serverHandshakeState) sendFinished(out []byte) error {
	c := hs.c

	if c.handshakeStatus >= handshakeStatusSendFinished {
		return nil
	}
	c.handshakeStatus = handshakeStatusSendFinished

	if err := c.writeChangeCipherRecord(); err != nil {
		return err
	}

	finished := new(finishedMsg)
	finished.verifyData = hs.finishedHash.serverSum(hs.masterSecret)
	if _, err := hs.c.writeHandshakeRecord(finished, &hs.finishedHash); err != nil {
		return err
	}

	copy(out, finished.verifyData)

	return nil
}

func (hs *serverHandshakeState) sendFinished2(out []byte) error {
	c := hs.c

	if c.handshakeStatus >= handshakeStatusSendFinished2 {
		return nil
	}
	c.handshakeStatus = handshakeStatusSendFinished2

	if err := c.writeChangeCipherRecord(); err != nil {
		return err
	}

	finished := new(finishedMsg)
	finished.verifyData = hs.finishedHash.serverSum(hs.masterSecret)
	if _, err := hs.c.writeHandshakeRecord(finished, &hs.finishedHash); err != nil {
		return err
	}

	copy(out, finished.verifyData)

	return nil
}

// processCertsFromClient takes a chain of client certificates either from a
// Certificates message or from a sessionState and verifies them. It returns
// the public key of the leaf certificate.
func (c *Conn) processCertsFromClient(certificate Certificate) error {
	certificates := certificate.Certificate
	certs := make([]*x509.Certificate, len(certificates))
	var err error
	for i, asn1Data := range certificates {
		if certs[i], err = x509.ParseCertificate(asn1Data); err != nil {
			c.sendAlert(alertBadCertificate)
			return errors.New("tls: failed to parse client certificate: " + err.Error())
		}
	}

	if len(certs) == 0 && requiresClientCert(c.config.ClientAuth) {
		c.sendAlert(alertBadCertificate)
		return errors.New("tls: client didn't provide a certificate")
	}

	if c.config.ClientAuth >= VerifyClientCertIfGiven && len(certs) > 0 {
		opts := x509.VerifyOptions{
			Roots:         c.config.ClientCAs,
			CurrentTime:   c.config.time(),
			Intermediates: x509.NewCertPool(),
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		}

		for _, cert := range certs[1:] {
			opts.Intermediates.AddCert(cert)
		}

		chains, err := certs[0].Verify(opts)
		if err != nil {
			c.sendAlert(alertBadCertificate)
			return &CertificateVerificationError{UnverifiedCertificates: certs, Err: err}
		}

		c.verifiedChains = chains
	}

	c.peerCertificates = certs
	c.ocspResponse = certificate.OCSPStaple
	c.scts = certificate.SignedCertificateTimestamps

	if len(certs) > 0 {
		switch certs[0].PublicKey.(type) {
		case *ecdsa.PublicKey, *rsa.PublicKey, ed25519.PublicKey:
		default:
			c.sendAlert(alertUnsupportedCertificate)
			return fmt.Errorf("tls: client certificate contains an unsupported public key of type %T", certs[0].PublicKey)
		}
	}

	if c.config.VerifyPeerCertificate != nil {
		if err := c.config.VerifyPeerCertificate(certificates, c.verifiedChains); err != nil {
			c.sendAlert(alertBadCertificate)
			return err
		}
	}

	return nil
}

func clientHelloInfo(ctx context.Context, c *Conn, clientHello *clientHelloMsg) *ClientHelloInfo {
	supportedVersions := clientHello.supportedVersions
	if len(clientHello.supportedVersions) == 0 {
		supportedVersions = supportedVersionsFromMax(clientHello.vers)
	}

	return &ClientHelloInfo{
		CipherSuites:      clientHello.cipherSuites,
		ServerName:        clientHello.serverName,
		SupportedCurves:   clientHello.supportedCurves,
		SupportedPoints:   clientHello.supportedPoints,
		SignatureSchemes:  clientHello.supportedSignatureAlgorithms,
		SupportedProtos:   clientHello.alpnProtocols,
		SupportedVersions: supportedVersions,
		Conn:              c.conn,
		config:            c.config,
		ctx:               ctx,
	}
}
