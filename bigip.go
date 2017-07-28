package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/docker/docker/api/types/swarm"
)

const (
	DG_PATH            = "/mgmt/tm/ltm/data-group/internal/"
	SERVICE_PATH_LABEL = "com.df.servicePath"
	BIGIP_HEADER       = "X-f5key"
	BIGIP_KEY_FILE     = "/run/secrets/bigip-key"
)

type Config struct {
	Host        string `json:"BIGIP_HOST"`
	DataGroup   string `json:"BIGIP_DG"`
	PoolPattern string `json:"BIGIP_RWP"`
}

type Record struct {
	Name string `json:"name,omitempty"`
	Data string `json:"data,omitempty"`
}

type DataGroup struct {
	Records []Record `json:"records,omitempty"`
}

type BigIp struct {
	Url      string
	Key      string
	Services map[string][]string
	Pattern  string
	Client   *http.Client
}

type BigIpClient interface {
	AddRoutes(services *[]swarm.Service) error
	RemoveRoutes(services *[]string) error
}

func (b *BigIp) AddRoutes(services *[]swarm.Service) error {
	errs := []error{}
	for _, s := range *services {
		//If servicepath label exists
		if label, ok := s.Spec.Labels[SERVICE_PATH_LABEL]; ok {
			//There might be multiple paths for a service
			label = strings.ToLower(label)
			paths := strings.Split(label, ",")
			log.Printf("Adding %v to %s", paths, b.Url)
			err := b.updateDataGroup(paths, false)
			if err != nil {
				log.Printf("%s", err.Error())
				errs = append(errs, err)
			} else {
				//Add service to cache
				b.Services[s.Spec.Name] = paths
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("Adding routes for at least one of the service failed")
	}
	return nil
}

func (b *BigIp) RemoveRoutes(services *[]string) error {
	errs := []error{}
	for _, s := range *services {
		if paths, ok := b.Services[s]; ok {
			log.Printf("Removing %v from %s", paths, b.Url)
			err := b.updateDataGroup(paths, true)
			if err != nil {
				log.Printf("%s", err.Error())
				errs = append(errs, err)
			} else {
				//Delete from cache
				delete(b.Services, s)
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("Removing routes for at least one of the service failed")
	}
	return nil
}

func (b *BigIp) updateDataGroup(paths []string, remove bool) error {
	//Get current records
	req, err := b.newRequest("GET", nil)
	resp, err := b.Client.Do(req)
	if err != nil {
		return fmt.Errorf("ERROR: Unable to get details of data group from url %s \n %s", b.Url, err.Error())
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	//If GET request is successful add or remove records
	if resp.StatusCode == http.StatusOK {
		//Unmarshal reponse into a struct
		dg := &DataGroup{}
		err := json.Unmarshal(body, dg)
		if err != nil {
			return fmt.Errorf("ERROR: Unable to unmarshal response from %s ", b.Url)
		}
		//Create records from paths
		records := b.getRecords(paths, b.Pattern)
		if remove {
			//Remove records from unmarshalled struct
			dg.Records = b.removeRecords(dg.Records, records)
		} else {
			//Append records to unmarshalled struct
			for _, r := range records {
				dg.Records = append(dg.Records, r)
			}
		}
		//Convert update struct to Json payload
		payload, err := json.Marshal(dg)
		if err != nil {
			return fmt.Errorf("ERROR: Unable to marshal %+v", dg)
		}
		//Update datagroup with updated records
		req, err := b.newRequest("PUT", payload)
		resp, err := b.Client.Do(req)
		if err != nil {
			return fmt.Errorf("ERROR: Unable to update data group at url %s \n %s", b.Url, err.Error())
		}
		defer resp.Body.Close()
		body, _ := ioutil.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("ERROR: Request %s returned status code %d\n%s", b.Url, resp.StatusCode, string(body[:]))
		}
	} else {
		return fmt.Errorf("ERROR: Request %s returned status code %d\n%s", b.Url, resp.StatusCode, string(body[:]))
	}
	return nil
}

func (b *BigIp) newRequest(method string, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(method, b.Url, bytes.NewBuffer(body))
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add(BIGIP_HEADER, b.Key)
	return req, err
}

func (b *BigIp) removeRecords(from []Record, remove []Record) []Record {
	removed := from[:0]
	for _, r := range from {
		if !b.containsRecord(remove, r) {
			removed = append(removed, r)
		}
	}
	return removed
}

func (b *BigIp) containsRecord(target []Record, candidate Record) bool {
	for _, t := range target {
		if t.Name == candidate.Name {
			return true
		}
	}
	return false
}

func (b *BigIp) getRecords(paths []string, pattern string) []Record {
	var records []Record
	for _, path := range paths {
		if len(path) > 0 {
			r := Record{}
			r.Name = path
			r.Data = pattern
			records = append(records, r)
		}
	}
	return records
}

func readConfig(configApi string) *Config {
	res, err := http.Get(configApi)
	checkErr(err)

	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		checkErr(fmt.Errorf("Config API at %s returned a non 200 OK response", configApi))
	}
	body, err := ioutil.ReadAll(res.Body)
	config := &Config{}
	err = json.Unmarshal(body, config)
	checkErr(err)
	return config
}

func checkErr(e error) {
	if e != nil {
		panic(e)
	}
}

func NewBigIp(configApi, keyFile string) *BigIp {

	key, err := ioutil.ReadFile(keyFile)
	checkErr(err)

	config := readConfig(configApi)

	var buff bytes.Buffer
	buff.WriteString(config.Host)
	buff.WriteString(DG_PATH)
	buff.WriteString(config.DataGroup)

	//Ignore https
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &BigIp{
		Url:      buff.String(),
		Key:      strings.TrimSpace(string(key)),
		Services: make(map[string][]string),
		Pattern:  config.PoolPattern,
		Client:   &http.Client{Transport: tr},
	}
}

func NewBigIpFromEnv() *BigIp {
	configApi := os.Getenv("DF_CONFIG_API")
	if len(configApi) == 0 {
		checkErr(fmt.Errorf("BigIp: Missing Config API Url"))
	}
	keyFile := os.Getenv("DF_BIGIP_KEY_FILE")
	if len(keyFile) == 0 {
		keyFile = BIGIP_KEY_FILE
	}
	return NewBigIp(configApi, keyFile)
}
