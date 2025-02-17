package command

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	mothership "github.com/kyma-project/control-plane/components/reconciler/pkg"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
)

func TestPurge(t *testing.T) {
	testCtx = context.Background()

	runtimeID := "05f9fa3f-0d8b-4cac-a738-6db1ac5124e2"
	expectedPath := fmt.Sprintf("/reconciliations/cluster/%s", runtimeID)

	errMsg := "Test error message"

	testCases := map[string]struct {
		ctx            context.Context
		wantErr        bool
		expectedErrMsg string
		mockResponse   func(t *testing.T) func(http.ResponseWriter, *http.Request)
	}{
		"Success": {
			ctx:     testCtx,
			wantErr: false,
			mockResponse: func(t *testing.T) func(http.ResponseWriter, *http.Request) {
				return func(writer http.ResponseWriter, r *http.Request) {
					assertRequests(t, r, expectedPath)
				}
			},
		},
		"Bad Request": {
			ctx:            testCtx,
			wantErr:        true,
			expectedErrMsg: errMsg,
			mockResponse: func(t *testing.T) func(http.ResponseWriter, *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					assertRequests(t, r, expectedPath)
					w.WriteHeader(http.StatusBadRequest)
					writeErrResponses(t, w, errMsg)
				}
			},
		},
		"Request Failed": {
			ctx:            testCtx,
			wantErr:        true,
			expectedErrMsg: errMsg,
			mockResponse: func(t *testing.T) func(http.ResponseWriter, *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					assertRequests(t, r, expectedPath)
					w.WriteHeader(http.StatusForbidden)
					writeErrResponses(t, w, errMsg)
				}
			},
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			//GIVEN
			mshipSvrMock := httptest.NewServer(http.HandlerFunc(testCase.mockResponse(t)))
			defer mshipSvrMock.Close()

			cmd := &reconciliationsPurgeCmd{
				reconcilerURL: mshipSvrMock.URL,
				ctx:           testCase.ctx,
				opts: reconciliationsPurgeOpts{
					runtimeID: runtimeID,
				},
			}

			//WHEN
			err := cmd.Run()

			//THEN
			if testCase.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), testCase.expectedErrMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func assertRequests(t *testing.T, r *http.Request, expectedPath string) {
	require.Equal(t, expectedPath, r.URL.Path)
	require.Equal(t, http.MethodDelete, r.Method)
}

func writeErrResponses(t *testing.T, w http.ResponseWriter, msg string) {
	resp := mothership.HTTPErrorResponse{Error: msg}
	out, err := json.Marshal(&resp)
	require.NoError(t, err)

	_, err = w.Write(out)
	require.NoError(t, err)
}
