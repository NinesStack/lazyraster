// Package urlsign contains a signed URL mechanism, where a URL can safely be
// passed through a third party and validated before being served. This is useful
// for passing a URL to a browser, for example, from one service and having a
// second service be certain the URL was as authorized. This is handled by
// generating a signing token for each URL based on all the other query parameters
// and the path. This does not validate the hostname or scheme from the passed
// URL. Expiration/bucket size is an external, agreed parameter between the
// services.
package urlsign

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// Yes, SHA1-HMAC is still considered secure, despite attacks on SHA-1 itself:
// https://crypto.stackexchange.com/questions/26510/why-is-hmac-sha1-still-considered-secure
var HmacAlgorithm = sha1.New

// timedSecret takes a secret, and generates a time-bucketed one time password.
// This is essentially RFC 6238 TOTP without the truncation of the output.
func timedSecret(secret []byte, baseTime int64) []byte {
	log.Debugf("\nBase time: %d\n", baseTime)

	mac := hmac.New(HmacAlgorithm, secret)
	buf := bytes.NewBuffer(nil)
	binary.Write(buf, binary.BigEndian, baseTime)
	mac.Write(buf.Bytes())
	log.Debugf("timePad: %x\n", mac.Sum(nil))
	return mac.Sum(nil)
}

// generateToken takes the secret generated by timedSecret and uses it to sign the
// url that is passed in, returning a hex-encoded string containing the signature.
func GenerateToken(secret string, bucketSize time.Duration, baseTime time.Time, reqUrl string) string {
	baseNano := baseTime.UnixNano() / int64(bucketSize)

	timePad := timedSecret([]byte(secret), baseNano)
	mac := hmac.New(HmacAlgorithm, timePad)
	mac.Write([]byte(reqUrl))
	hashed := mac.Sum(nil)
	return fmt.Sprintf("%x", hashed)
}

// isValidSignature takes a signed URL, grabs the token, generates an HMAC for the
// URL as expected, and compares the results. To work properly, this method
// assumes that the arguments in the URL are +sorted+ in string order.
// Additionally, it will test the current timeBucket and the previous and next
// buckets providing a 3*timeBucket window of validity for each signature.
func IsValidSignature(secret string, bucketSize time.Duration, baseTime time.Time, reqUrl string) bool {
	// The list of time baseTimes we'll validate this for
	bucketsToCheck := []time.Time{
		baseTime,                     // This bucket
		baseTime.Add(0 - bucketSize), // The previous one
		baseTime.Add(bucketSize),     // The next one
	}

	// Parse the URL, get the token, and extract the pieces that are signed
	parsed, err := url.Parse(reqUrl)
	if err != nil {
		log.Warnf("Unparseable URL in isValidSignature(): %s", reqUrl)
		return false
	}

	query := parsed.Query()
	tmpToken, ok := query["token"]
	if !ok {
		return false
	}

	// All query params are slices, but we want the first entry
	reqToken := tmpToken[0]

	// Now that we grabbed it, remove it
	delete(query, "token")
	var params []string
	for k, v := range query {
		params = append(params, k+"="+v[0])
	}

	// The hash will return key-value pairs in random order.
	// We sort them to guarantee repeatability.
	sort.Strings(params)

	// We re-assemble only the parts that are signed
	reconstituted := fmt.Sprintf("%s?%s", parsed.Path, strings.Join(params, "&"))

	// Check each valid time bucket, starting wtih the current one
	for _, bucket := range bucketsToCheck {
		expectedToken := GenerateToken(secret, bucketSize, bucket, reconstituted)
		log.Debugf("Expected token: %s", expectedToken)
		if reqToken == expectedToken {
			return true
		}
	}

	return false
}
