// Package harness runs the built binary for the black-box suites. It starts
// and stops the process, validates every response against the OpenAPI
// contract, and decodes the status stream.
//
//	bin, contract, cleanup, err := harness.Setup() // once, in TestMain
//	p := harness.Start(t, bin, "MINGEST_DATASET_SOURCE="+url)
//	resp, err := contract.Client().Do(req)         // held to openapi.yaml
//	events, err := contract.ReadEvents(resp.Body)  // a text/event-stream
//	p.Stop(t)                                      // SIGTERM, exit code 0
//	err = contract.Check(excluded)                 // after the suite
package harness
