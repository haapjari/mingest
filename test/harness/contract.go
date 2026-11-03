package harness

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
	liberrors "github.com/pb33f/libopenapi-validator/errors"
	"github.com/pb33f/libopenapi-validator/schema_validation"
	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
)

const (
	problemJSON = "application/problem+json"
	eventStream = "text/event-stream"
)

// Contract holds the OpenAPI document the black-box suites test against.
type Contract struct {
	doc       *v3.Document
	validator validator.Validator
	schemas   schema_validation.SchemaValidator

	mu   sync.Mutex
	seen map[Response]string
}

// LoadContract reads the OpenAPI document at path.
func LoadContract(path string) (*Contract, error) {
	spec, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	model, err := v3Model(spec)
	if err != nil {
		return nil, err
	}

	return &Contract{
		doc:       model,
		validator: validator.NewValidatorFromV3Model(model),
		schemas:   schema_validation.NewSchemaValidator(),
		seen:      map[Response]string{},
	}, nil
}

// Client returns an HTTP client that validates every response against the
// contract and records it for Check. A response the contract doesn't allow
// is returned as an error.
func (c *Contract) Client() *http.Client {
	return &http.Client{Transport: &transport{contract: c}}
}

// check buffers resp's body and validates resp.
func (c *Contract) check(req *http.Request, resp *http.Response) error {
	if err := buffer(resp); err != nil {
		return err
	}

	if err := c.validate(req, resp); err != nil {
		return err
	}

	return nil
}

func (c *Contract) validate(req *http.Request, resp *http.Response) error {
	item := c.doc.Paths.PathItems.GetOrZero(req.URL.Path)
	if item == nil {
		return c.validateUnknownPath(req, resp)
	}

	if operation(item, req.Method) == nil {
		return c.validateUnlistedMethod(req, resp, item)
	}

	return c.validateDocumented(req, resp)
}

// validateUnknownPath holds a path the contract doesn't list to the 404
// problem that x-problem-types promises for it.
func (c *Contract) validateUnknownPath(
	req *http.Request, resp *http.Response,
) error {
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf(
			"%s %s: an unknown path gave %d, want 404",
			req.Method, req.URL.Path, resp.StatusCode,
		)
	}

	return c.validateProblem(req, resp)
}

// validateUnlistedMethod expects a 405 with an Allow header when the path
// doesn't list the method, then checks the response against the path's
// documented 405.
func (c *Contract) validateUnlistedMethod(
	req *http.Request, resp *http.Response, item *v3.PathItem,
) error {
	if resp.StatusCode != http.StatusMethodNotAllowed ||
		resp.Header.Get("Allow") == "" {
		return fmt.Errorf(
			"%s %s: an unlisted method gave %d with Allow %q, "+
				"want 405 with Allow",
			req.Method, req.URL.Path, resp.StatusCode,
			resp.Header.Get("Allow"),
		)
	}

	listed := req.Clone(req.Context())
	listed.Method = strings.ToUpper(item.GetOperations().First().Key())

	return c.validateDocumented(listed, resp)
}

// validateProblem checks that resp is a problem+json body valid against the
// Problem schema.
func (c *Contract) validateProblem(
	req *http.Request, resp *http.Response,
) error {
	if mediaType(resp) != problemJSON {
		return fmt.Errorf(
			"%s %s: content type %q, want %s",
			req.Method, req.URL.Path, mediaType(resp), problemJSON,
		)
	}

	return c.validateSchema(describe(req), "Problem", buffered(resp))
}

// validateDocumented validates resp with libopenapi-validator, against the
// operation the contract documents for req.
func (c *Contract) validateDocumented(
	req *http.Request, resp *http.Response,
) error {
	// The validator reads the body and puts it back.
	ok, errs := c.validator.ValidateHttpResponse(req, resp)
	if ok {
		return nil
	}

	return validationError(describe(req), errs)
}

// validateSchema checks data against the named schema in components. what
// names the data in the error.
func (c *Contract) validateSchema(what, name string, data []byte) error {
	ok, errs := c.schemas.ValidateSchemaBytes(c.schema(name), data)
	if ok {
		return nil
	}

	return validationError(what, errs)
}

func (c *Contract) schema(name string) *base.Schema {
	return c.doc.Components.Schemas.GetOrZero(name).Schema()
}

type transport struct {
	contract *Contract
}

// RoundTrip sends req and validates the response. The body is read whole
// for validation and handed back to the caller unread. An event stream isn't
// read: its media type has no schema, and ReadEvents checks its events.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	if err := t.contract.check(req, resp); err != nil {
		return nil, errors.Join(err, resp.Body.Close())
	}

	t.contract.record(req, resp)

	return resp, nil
}

// replayable is a read body kept in memory.
type replayable struct {
	*bytes.Reader
	data []byte
}

// Close implements io.Closer so replayable can stand in as an
// http.Response.Body. The body was already drained and closed in
// buffer, so there is nothing left to release.
func (*replayable) Close() error { return nil }

func v3Model(spec []byte) (*v3.Document, error) {
	doc, err := libopenapi.NewDocument(spec)
	if err != nil {
		return nil, err
	}

	model, err := doc.BuildV3Model()
	if err != nil {
		return nil, err
	}

	return &model.Model, nil
}

// buffer reads resp's body so it can be read once by the validator and once
// by the caller. It leaves an event stream alone.
func buffer(resp *http.Response) error {
	if mediaType(resp) == eventStream {
		return nil
	}

	data, err := io.ReadAll(resp.Body)
	if err = errors.Join(err, resp.Body.Close()); err != nil {
		return err
	}

	resp.Body = &replayable{Reader: bytes.NewReader(data), data: data}

	return nil
}

// buffered returns the body buffer read, or nil for a stream.
func buffered(resp *http.Response) []byte {
	if body, ok := resp.Body.(*replayable); ok {
		return body.data
	}

	return nil
}

// operation returns the operation item documents for method, or nil.
func operation(item *v3.PathItem, method string) *v3.Operation {
	return item.GetOperations().GetOrZero(strings.ToLower(method))
}

// listedOperation returns the operation for method, or for a method item
// doesn't list, the operation whose 405 answers it.
func listedOperation(item *v3.PathItem, method string) *v3.Operation {
	if op := operation(item, method); op != nil {
		return op
	}

	return item.GetOperations().First().Value()
}

// mediaType returns resp's media type without its parameters, or "" when it
// has none.
func mediaType(resp *http.Response) string {
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))

	return mt
}

// describe names req in an error: its method and path.
func describe(req *http.Request) string {
	return req.Method + " " + req.URL.Path
}

func validationError(what string, errs []*liberrors.ValidationError) error {
	msgs := make([]error, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, errors.New(e.Error()))
	}

	return fmt.Errorf("%s breaks the contract: %w", what, errors.Join(msgs...))
}
