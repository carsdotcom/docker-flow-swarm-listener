package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	service "./service"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

const (
	SERVICE_NAME = "test-service"
	PATH         = "/test-path"
	DG           = "test-dg"
	PATTERN      = "test-pattern"
)

type BigIpTestSuite struct {
	suite.Suite
	bigIPKeyFile      string
	goodConfigServer  *httptest.Server
	badConfigServer   *httptest.Server
	errorConfigServer *httptest.Server
}

func TestBigIpTestSuite(t *testing.T) {
	s := new(BigIpTestSuite)
	suite.Run(t, s)
}

func (s *BigIpTestSuite) TearDownSuite() {
	s.goodConfigServer.Close()
	s.badConfigServer.Close()
	s.errorConfigServer.Close()
}

func (s *BigIpTestSuite) SetupSuite() {
	fmt.Printf("Setting up tests\n")
	// create test files and directories
	os.MkdirAll("/tmp/secrets", 0755)
	ioutil.WriteFile("/tmp/secrets/bigip-test-key", []byte("test-key-value"), 0755)
	s.bigIPKeyFile = "/tmp/secrets/bigip-test-key"

	//set up good config server
	errorBigIPServer := goodServer(DG, []byte(`{bad-json : [{"invalid-name":"`+PATH+`", "data":"`+PATTERN+`"}]}`))
	s.errorConfigServer = configServer(errorBigIPServer.URL, DG, PATTERN, "service")

	goodBigIpServer := goodServer(DG, []byte(`{"records" : [{"name":"`+PATH+`", "data":"`+PATTERN+`"}]}`))
	s.goodConfigServer = configServer(goodBigIpServer.URL, DG, PATTERN, "service")

	//set up bad config server
	badBigIpServer := badServer()
	s.badConfigServer = configServer(badBigIpServer.URL, DG, PATTERN, "service")
}

func (s *BigIpTestSuite) Test_NewBigIp() {
	bigIp := NewBigIp(s.goodConfigServer.URL, s.bigIPKeyFile)
	assert.NotNil(s.T(), bigIp, "bigIp should be an object")
}

func (s *BigIpTestSuite) Test_NewBigIp_Creates_Http_Client() {
	bigIp := NewBigIp(s.goodConfigServer.URL, s.bigIPKeyFile)
	assert.NotNil(s.T(), bigIp, "bigIp should be an object")
	assert.NotNil(s.T(), bigIp.Client, "should create a http client")
}

func (s *BigIpTestSuite) Test_NewBigIpFromEnv_ReturnsErr_On_MissingFlags() {
	assert.Panics(s.T(), func() { NewBigIpFromEnv() }, "The code did not panic")
}

func (s *BigIpTestSuite) Test_NewBigIpFromEnv() {
	os.Setenv("DF_CONFIG_API", s.goodConfigServer.URL)
	os.Setenv("DF_BIGIP_KEY_FILE", s.bigIPKeyFile)
	bigIp := NewBigIpFromEnv()
	os.Unsetenv("DF_CONFIG_API")
	os.Unsetenv("DF_BIGIP_KEY_FILE")
	assert.NotNil(s.T(), bigIp, "should return bigIp")
	assert.Equal(s.T(), bigIp.Key, "test-key-value", "key should be set")
	assert.Equal(s.T(), bigIp.Pattern, "test-pattern", "pattern should be set")
	assert.True(s.T(), len(bigIp.Url) > 0, "Url should be set")
	assert.NotNil(s.T(), bigIp.Client, "should create a http client")
}

func (s *BigIpTestSuite) Test_AddRemoveRoutes_ReturnErr_IfStatusNot200OK() {
	bigIp := NewBigIp(s.badConfigServer.URL, s.bigIPKeyFile)
	assert.NotNil(s.T(), bigIp, "should return bigIp")
	labels := make(map[string]string)
	labels["com.df.servicePath"] = "true"
	err := bigIp.AddRoutes(s.getSwarmServices("test", labels))
	s.Error(err)
	bigIp.Services["test"] = []string{"/test"}
	err = bigIp.RemoveRoutes(&[]string{"test"})
	s.Error(err)
}

func (s *BigIpTestSuite) Test_Add_Remove_Routes() {
	bigIp := NewBigIp(s.goodConfigServer.URL, s.bigIPKeyFile)
	assert.NotNil(s.T(), bigIp, "should return bigIp")
	labels := make(map[string]string)
	labels["com.df.servicePath"] = PATH
	err := bigIp.AddRoutes(s.getSwarmServices(SERVICE_NAME, labels))
	assert.Nil(s.T(), err, "should not return err")
	assert.True(s.T(), len(bigIp.Services) > 0, "cache size should be > 0")
	value, ok := bigIp.Services[SERVICE_NAME]
	assert.True(s.T(), ok, "service should be added to cache")
	assert.Equal(s.T(), value[0], PATH, "path should be added to cache")

	err = bigIp.RemoveRoutes(&[]string{SERVICE_NAME})
	assert.Nil(s.T(), err, "should not return err")
	assert.True(s.T(), len(bigIp.Services) == 0, "cache size should be > 0")
}

func (s *BigIpTestSuite) Test_UpdateDataGroup_Marshall_Error() {
	bigIp := NewBigIp(s.errorConfigServer.URL, s.bigIPKeyFile)
	assert.NotNil(s.T(), bigIp, "should return bigIp")
	labels := make(map[string]string)
	labels["com.df.servicePath"] = PATH
	err := bigIp.AddRoutes(s.getSwarmServices(SERVICE_NAME, labels))
	s.Error(err)
}

func (s *BigIpTestSuite) Test_NewRequest() {
	bigIp := NewBigIp(s.goodConfigServer.URL, s.bigIPKeyFile)
	req, err := bigIp.newRequest("GET", nil)
	assert.Nil(s.T(), err, "newRequest with GET should not result in err")
	assert.NotNil(s.T(), req, "newRequest with GET should not return req object")
	val := req.Header.Get(BIGIP_HEADER)
	assert.True(s.T(), val == "test-key-value", "newRequest sets the BIGIP_HEADER")
}

func (s *BigIpTestSuite) Test_GetRecords() {
	b := NewBigIp(s.goodConfigServer.URL, s.bigIPKeyFile)
	paths := []string{"/test-1", "/test-2"}
	pattern := "test-pattern"

	records := b.getRecords(paths, pattern)

	assert.NotNil(s.T(), records, "records should not be nil")
	assert.Equal(s.T(), len(records), 2, "len(records) should be equal to 2")
}

func (s *BigIpTestSuite) Test_ContainsRecords() {
	b := NewBigIp(s.goodConfigServer.URL, s.bigIPKeyFile)
	records := []Record{
		Record{Name: "/test-1", Data: "test-pattern"},
		Record{Name: "/test-2", Data: "test-pattern"},
		Record{Name: "/test-3", Data: "test-pattern"},
		Record{Name: "/test-4", Data: "test-pattern"},
	}
	record := Record{Name: "/test-3", Data: "test-pattern"}
	assert.True(s.T(), b.containsRecord(records, record), "containsRecord should return true")
	record = Record{Name: "/test-5", Data: "test-pattern"}
	assert.False(s.T(), b.containsRecord(records, record), "containsRecord should return false")
}

func (s *BigIpTestSuite) Test_RemovedRecords() {
	b := NewBigIp(s.goodConfigServer.URL, s.bigIPKeyFile)
	records := []Record{
		Record{Name: "/test-1", Data: "test-pattern"},
		Record{Name: "/test-2", Data: "test-pattern"},
		Record{Name: "/test-3", Data: "test-pattern"},
		Record{Name: "/test-4", Data: "test-pattern"},
	}
	remove := []Record{
		Record{Name: "/test-1", Data: "test-pattern"},
		Record{Name: "/test-2", Data: "test-pattern"},
	}
	removed := b.removeRecords(records, remove)
	assert.True(s.T(), len(removed) > 0, "removed records should be > 0")
	assert.True(s.T(), len(removed) == 2, "removed records should be 2")
}

func badServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
}

func configServer(bigIpHost, dataGroup, pattern, tier string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actualPath := r.URL.Path
		if r.Method == "GET" {
			switch actualPath {
			default:
				w.WriteHeader(http.StatusOK)
				w.Header().Set("Content-Type", "application/json")
				payload := `
        { "BIGIP_HOST":"` + bigIpHost + `",
          "BIGIP_DG":"` + dataGroup + `",
          "BIGIP_RWP":"` + pattern + `"
        }
        `
				w.Write([]byte(payload))
			}
		}
	}))
}

func goodServer(dg string, payload []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actualPath := r.URL.Path
		if r.Method == "GET" {
			switch actualPath {
			case "/mgmt/tm/ltm/data-group/internal/" + dg:
				w.WriteHeader(http.StatusOK)
				w.Header().Set("Content-Type", "application/json")
				w.Write(payload)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}
	}))
}

func (s *BigIpTestSuite) getSwarmServices(name string, labels map[string]string) *[]swarm.Service {
	ann := swarm.Annotations{
		Name:   name,
		Labels: labels,
	}
	spec := swarm.ServiceSpec{
		Annotations: ann,
	}
	serv := swarm.Service{
		Spec: spec,
	}
	return &[]service.SwarmService{
		Service: serv,
	}
}
