package configstore

import (
	"fmt"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

var validConfig = []byte(`---
logpath: "/dev/stderr"
loglevel: "DEBUG"
modelplugins:
  - id: "trivial"
    path: "../testdata/plugins/model/trivial.so"
    weight: 1
    threshold: 0.5
    params:
      d: "sds"
      b: "dnid"
      e: "dofnno"
    plugintype: "RequestHeaders"
    mode: "sync"
  - id: "trivial2"
    path: "../testdata/plugins/model/trivial2.so"
    weight: 2
    threshold: 0.1
    params:
      a: "sdsds"
      b: "sdfjdnid"
      c: "kfoskdofnno"
    plugintype: "RequestHeaders"
decisionplugins:
  - id: "test"
    path: "../testdata/plugins/decision/test.so"
    wafweight: 0.5
    decisionbalance: 0.5
    params:
      ssdaf: "sdsds"
      dsfb: "sdfjdnid"
      csfd: "kfoskdofnno"
`)

func initialize(configuration []byte) error {
	cs, err := Get()
	if err != nil {
		return err
	}
	var aux ConfigFileData
	err = yaml.Unmarshal(configuration, &aux)
	if err != nil {
		return err
	}
	err = cs.SetConfig(aux)
	if err != nil {
		return err
	}
	return nil
}

func TestLoadConfigYamlEmpty(t *testing.T) {
	_, err := New()
	if err != nil {
		t.Error(err)
	}

	defer Clean()

	err = initialize([]byte(`---`))
	if err == nil {
		t.Error("empty config does not return error")
	}
}

func TestLoadConfigYamlValid(t *testing.T) {
	_, err := New()
	if err != nil {
		t.Error(err)
	}

	defer Clean()

	err = initialize(validConfig)
	if err != nil {
		t.Errorf("valid config returned error: %v", err)
	}
}

func TestLoadConfigYamlInvalid(t *testing.T) {
	_, err := New()
	if err != nil {
		t.Error(err)
	}

	defer Clean()

	err = initialize([]byte(`()=)(/&/()~@#~½¬{[{½¬½---sfdjlskjfs#@~sjdfa`))

	if err == nil {
		t.Error("invalid config does not return error")
	}
}

func TestLoadConfigYamlLogLevel(t *testing.T) {
	_, err := New()
	if err != nil {
		t.Error(err)
	}

	defer Clean()

	values := []string{
		"a",
		"4",
		"0",
	}

	for _, v := range values {
		config := `---
logpath: "/dev/null"
loglevel: ` + v
		err = initialize([]byte(config))
		if err == nil {
			t.Errorf("invalid log level %v does not return error", v)
		}
	}
}

func TestLoadConfigYamlPluginType(t *testing.T) {
	cs, err := New()
	if err != nil {
		t.Error(err)
	}

	defer Clean()

	err = initialize([]byte(`---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: InvalidPluginType
`))
	if err == nil {
		t.Errorf("invalid plugin type does not return error")
	}

	err = initialize([]byte(`---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: ""
`))
	if err == nil {
		t.Errorf("empty plugin type does not return error")
	}

	err = initialize([]byte(`---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/nonexistent.so"
    plugintype: "RequestHeaders"
`))
	if err == nil {
		t.Errorf("nonexistent model plugin path does not return error")
	}

	err = initialize([]byte(`---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: ""
    plugintype: "RequestHeaders"
`))
	if err == nil {
		t.Errorf("empty plugin path does not return error")
	}

	err = initialize([]byte(`---
loglevel: ERROR
logpath: /dev/null
decisionplugins:
  - id: "test"
    path: ""
`))
	if err == nil {
		t.Errorf("empty decision plugin path does not return error")
	}

	err = initialize([]byte(`---
loglevel: ERROR
logpath: /dev/null
decisionplugins:
  - id: "testplugin"
    path: "../testdata/plugins/decision/nonexistent.so"
`))
	if err == nil {
		t.Errorf("nonexistent decision plugin path does not return error")
	}

	values := []string{
		"RequestHeaders",
		"RequestBody",
		"AllRequest",
		"ResponseHeaders",
		"ResponseBody",
		"AllResponse",
		"Everything",
	}

	for _, v := range values {
		config := `---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: "` + v + `"
`
		err = initialize([]byte(config))
		if err != nil {
			t.Errorf("Plugin type %s returns error: %v", v, err)
		}

		if fmt.Sprint(cs.ModelPlugins["testplugin"].PluginType) != v {
			t.Errorf("Stored plugin type is %v, expected %v", cs.ModelPlugins["testplugin"].PluginType, v)
		}
	}
}

func TestInvalidLogging(t *testing.T) {
	_, err := New()
	if err != nil {
		t.Error(err)
	}

	defer Clean()

	err = initialize([]byte(`---
loglevel: INVALIDLOGLEVEL
logpath: /dev/null
`))
	if err == nil {
		t.Errorf("invalid log level does not return error")
	}

	if _, err = os.Stat("./configstore_test.log"); err == nil {
		err = os.Remove("./configstore_test.log")
		if err != nil {
			t.Errorf("could not remove ./configstore_test.log")
		}
	}

	err = initialize([]byte(`---
loglevel: ERROR
logpath: ./configstore_test.log`))

	if err != nil {
		t.Errorf("Error loading config  with nonexistent file: %v", err)
	}

	err = initialize([]byte(`---
loglevel: ERROR
logpath: /usr/configstore_test.log`))

	if err == nil {
		t.Errorf("non existent log file in directory without permissions does not rise error")
	}

}
