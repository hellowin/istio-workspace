package e2e

import (
	"context"
	"io/ioutil"
	"net/http"

	"emperror.dev/errors"
)

// GetBodyWithHeaders calls GET on a given URL with a specific set request headers
// and returns its body or error in case there's one.
func GetBodyWithHeaders(rawURL string, headers map[string]string) (string, error) {
	req, err := http.NewRequestWithContext(context.Background(), "GET", rawURL, nil)
	if err != nil {
		return "", errors.Wrap(err, "failed creating request")
	}
	if v, f := headers["Host"]; f {
		req.Host = v
	}
	for k, v := range headers {
		req.Header[k] = []string{v}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", errors.WrapWithDetails(err, "failed executing HTTP call", "url", req.URL.String())
	}
	defer resp.Body.Close()
	content, _ := ioutil.ReadAll(resp.Body)

	return string(content), nil
}
