package proxy

import (
	"context"
	"fmt"
	"github.com/projectriff/streaming-http-adapter/pkg/proxy/mocks"
	"github.com/projectriff/streaming-http-adapter/pkg/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func Test_invokeGrpc_input_startFrame(t *testing.T) {
	riffClient, invokeClient := mockRiffClient()
	p := &proxy{riffClient: riffClient}

	request, _ := http.NewRequest("POST", "/", strings.NewReader(""))
	request.Header.Add("accept", "text/plain")
	p.invokeGrpc(httptest.NewRecorder(), request)

	inputSignals := inputSignals(invokeClient.Calls)
	startFrame := inputSignals[0].GetStart()
	assert.Equal(t, []string{"text/plain"}, startFrame.ExpectedContentTypes)
	assert.Equal(t, []string{"in"}, startFrame.InputNames)
	assert.Equal(t, []string{"out"}, startFrame.OutputNames)
}

func Test_invokeGrpc_input_dataFrame(t *testing.T) {
	riffClient, invokeClient := mockRiffClient()
	p := &proxy{riffClient: riffClient}

	request, _ := http.NewRequest("POST", "/", strings.NewReader("some body"))
	request.Header.Add("content-type", "text/plain")
	request.Header.Add("x-custom-header", "header-value")
	p.invokeGrpc(httptest.NewRecorder(), request)

	inputSignals := inputSignals(invokeClient.Calls)
	dataFrame := inputSignals[1].GetData()
	assert.Equal(t, "some body", string(dataFrame.Payload))
	assert.Equal(t, "text/plain", dataFrame.ContentType)
	assert.Contains(t, dataFrame.Headers, "X-Custom-Header")
	assert.Equal(t, dataFrame.Headers["X-Custom-Header"], "header-value")
}

func Test_invokeGrpc_output(t *testing.T) {
	riffClient, _ := mockRiffClientWithResponse("<data>some response</data>", "application/xml")
	p := &proxy{riffClient: riffClient}

	request, _ := http.NewRequest("POST", "/", strings.NewReader(""))
	responseRecorder := httptest.NewRecorder()
	p.invokeGrpc(responseRecorder, request)

	assert.Equal(t, "<data>some response</data>", responseRecorder.Body.String())
	assert.Equal(t, "application/xml", responseRecorder.Header().Get("Content-Type"))
}

func Test_invokeGrpc_wiring(t *testing.T) {
	riffClient, invokeClient := mockRiffClient()
	p := &proxy{riffClient: riffClient}

	request, _ := http.NewRequest("POST", "/", strings.NewReader("some body"))
	p.invokeGrpc(httptest.NewRecorder(), request)

	riffClient.AssertExpectations(t)
	invokeClient.AssertExpectations(t)
}

func Test_not_acceptable_media_type(t *testing.T) {
	accept := "text/zglorbf"
	errorMsg := fmt.Sprintf("Invoker: Not Acceptable: unrecognized output #0's content-type %s", accept)
	riffClient, _ := mockRiffClientWithError(codes.InvalidArgument, errorMsg)
	p := &proxy{riffClient: riffClient}
	request, _ := http.NewRequest("POST", "/", strings.NewReader("some body"))
	request.Header.Set("Accept", accept)

	responseRecorder := httptest.NewRecorder()
	p.invokeGrpc(responseRecorder, request)

	assert.Equal(t, http.StatusNotAcceptable, responseRecorder.Code)
	assert.Equal(t, "text/plain", responseRecorder.Header().Get("Content-Type"))
	assert.Equal(t, errorMsg+"\n", responseRecorder.Body.String())
}

func Test_unsupported_request_method(t *testing.T) {
	riffClient, _ := mockRiffClient()
	p := &proxy{riffClient: riffClient}

	request, _ := http.NewRequest("GET", "/", strings.NewReader(""))
	responseRecorder := httptest.NewRecorder()
	p.invokeGrpc(responseRecorder, request)

	assert.Equal(t, http.StatusNotImplemented, responseRecorder.Code)
}

func Test_unsupported_request_path(t *testing.T) {
	riffClient, _ := mockRiffClient()
	p := &proxy{riffClient: riffClient}

	request, _ := http.NewRequest("POST", "/nope/", strings.NewReader(""))
	responseRecorder := httptest.NewRecorder()
	p.invokeGrpc(responseRecorder, request)

	assert.Equal(t, http.StatusNotImplemented, responseRecorder.Code)
}

func Test_unsupported_content_type(t *testing.T) {
	contentType := "text/zglorbf"
	errorMsg := fmt.Sprintf("Invoker: Unsupported Media Type: unsupported input #0's content-type %s", contentType)
	riffClient, _ := mockRiffClientWithError(codes.InvalidArgument, errorMsg)
	p := &proxy{riffClient: riffClient}
	request, _ := http.NewRequest("POST", "/", strings.NewReader("some body"))
	request.Header.Set("Content-Type", contentType)

	responseRecorder := httptest.NewRecorder()
	p.invokeGrpc(responseRecorder, request)

	assert.Equal(t, http.StatusUnsupportedMediaType, responseRecorder.Code)
	assert.Equal(t, "text/plain", responseRecorder.Header().Get("Content-Type"))
	assert.Equal(t, errorMsg+"\n", responseRecorder.Body.String())
}

func inputSignals(calls []mock.Call) []*rpc.InputSignal {
	var inputSignals []*rpc.InputSignal
	for _, call := range calls {
		if call.Method == "Send" {
			signal := call.Arguments.Get(0).(*rpc.InputSignal)
			inputSignals = append(inputSignals, signal)
		}
	}
	return inputSignals
}

func mockRiffClient() (*mocks.RiffClient, *mocks.Riff_InvokeClient) {
	return mockRiffClientWithResponse("", "")
}

func mockRiffClientWithResponse(outputBody string, contentType string) (*mocks.RiffClient, *mocks.Riff_InvokeClient) {
	riffClient := &mocks.RiffClient{}
	invokeClient := &mocks.Riff_InvokeClient{}
	riffClient.On("Invoke", context.Background()).Return(invokeClient, nil)
	invokeClient.On("Send", mock.Anything).Return(nil)
	invokeClient.On("CloseSend").Return(nil)
	invokeClient.On("Recv").Return(outputSignal(outputBody, contentType), nil).Once()
	invokeClient.On("Recv").Return(nil, io.EOF)
	return riffClient, invokeClient
}

func mockRiffClientWithError(code codes.Code, msg string) (*mocks.RiffClient, *mocks.Riff_InvokeClient) {
	riffClient := &mocks.RiffClient{}
	invokeClient := &mocks.Riff_InvokeClient{}
	riffClient.On("Invoke", context.Background()).Return(invokeClient, nil)
	invokeClient.On("Send", mock.MatchedBy(isStartSignal)).Return(nil)
	invokeClient.On("Send", mock.MatchedBy(isDataSignal)).Return(status.Error(code, msg))
	return riffClient, invokeClient
}

func isDataSignal(inputSignal *rpc.InputSignal) bool {
	return inputSignal.GetData() != nil
}

func isStartSignal(inputSignal *rpc.InputSignal) bool {
	return inputSignal.GetStart() != nil
}

func outputSignal(outputBody string, contentType string) *rpc.OutputSignal {
	return &rpc.OutputSignal{
		Frame: &rpc.OutputSignal_Data{
			Data: &rpc.OutputFrame{
				Payload:     []byte(outputBody),
				ContentType: contentType,
			},
		},
	}
}
