package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/stretchr/testify/require"
)

func init() {
	openapi3filter.RegisterBodyDecoder("text/html", openapi3filter.PlainBodyDecoder)
}

func described() (*openapi3.T, error) {
	data, err := os.ReadFile("../../api/swagger.json")
	if err != nil {
		return nil, err
	}
	var v2 openapi2.T
	if err := json.Unmarshal(data, &v2); err != nil {
		return nil, err
	}
	doc, err := openapi2conv.ToV3(&v2)
	if err != nil {
		return nil, err
	}
	doc.Servers = nil
	return doc, doc.Validate(context.Background())
}

func checkResponse(req *http.Request, status int, header http.Header, body []byte) error {
	doc, err := described()
	if err != nil {
		return err
	}
	rt, err := gorillamux.NewRouter(doc)
	if err != nil {
		return err
	}
	route, params, err := rt.FindRoute(req)
	if err != nil {
		return nil
	}
	opts := &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, IncludeResponseStatus: true}
	return openapi3filter.ValidateResponse(req.Context(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route, Options: opts},
		Status:                 status, Header: header, Body: io.NopCloser(bytes.NewReader(body)), Options: opts,
	})
}

func TestOpenAPIValid(t *testing.T) {
	_, err := described()
	require.NoError(t, err, "описание API не прошло проверку OpenAPI")
}

func TestOpenAPIMatchesReadme(t *testing.T) {
	doc, err := described()
	require.NoError(t, err, "описание API не прочитано")
	var described []string
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			described = append(described, method+" "+path)
		}
	}
	readme, err := os.Open("../../README.md")
	require.NoError(t, err, "README не открыт")
	defer func() { _ = readme.Close() }()
	var listed []string
	section, block := false, false
	for lines := bufio.NewScanner(readme); lines.Scan(); {
		line := lines.Text()
		switch {
		case strings.HasPrefix(line, "## "):
			section = line == "## Документация"
		case section && strings.HasPrefix(line, "```"):
			block = !block
		case section && block:
			fields := strings.Fields(line)
			listed = append(listed, fields[0]+" "+fields[1])
		}
	}
	slices.Sort(described)
	slices.Sort(listed)
	require.Equal(t, listed, described, "описание API расходится с маршрутами README")
}

func TestOpenAPIRejects(t *testing.T) {
	cases := []struct {
		name, kind, body string
		status           int
		want             string
	}{
		{"тело", "application/json", `{"id":1}`, http.StatusOK, "response body doesn't match schema"},
		{"код", "application/json", `{}`, http.StatusTeapot, "status is not supported"},
		{"тип", "text/csv", `id`, http.StatusOK, "header Content-Type has unexpected value"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", c.kind)
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		resp, err := srv.Client().Get(srv.URL + "/api/v1/avatars/x/metadata")
		require.NoError(t, err, "сервер не ответил")
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		srv.Close()
		require.NoError(t, err, "ответ не прочитан")
		err = checkResponse(resp.Request, resp.StatusCode, resp.Header, body)
		require.ErrorContains(t, err, c.want, "ответ не по описанию прошёл, случай %s", c.name)
	}
}
