package devicecatalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedCatalogIsValid(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.CatalogVersion == "" || len(catalog.Definitions) < 10 {
		t.Fatalf("embedded catalog = version %q with %d definitions", catalog.CatalogVersion, len(catalog.Definitions))
	}
}

func TestCatalogIgnoresGenericVendorTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.1.20", []string{
		"www.googleapis.com", "api.apple.com", "login.microsoftonline.com", "plex.tv",
	})
	if prediction.DeviceType != "Unknown" || prediction.Confidence != "unknown" {
		t.Fatalf("generic traffic predicted %#v", prediction)
	}
}

func TestCatalogUsesDistinctiveSignalsAndExplainsResult(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.1.20", []string{
		"connectivitycheck.gstatic.com", "mtalk.google.com",
	})
	if prediction.DeviceType != "Android Device" || prediction.Confidence != "medium" {
		t.Fatalf("prediction = %#v", prediction)
	}
	if prediction.DefinitionID != "android-device" || len(prediction.Evidence) != 2 {
		t.Fatalf("prediction evidence = %#v", prediction)
	}
	if prediction.CatalogVersion != catalog.CatalogVersion || prediction.SignalHash == "" || prediction.EvaluatedAt == "" {
		t.Fatalf("prediction metadata = %#v", prediction)
	}
}

func TestCatalogRequiresMultipleConsoleSpecificXboxSignals(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	insufficient := catalog.Predict("", "192.168.1.40", []string{
		"device.auth.xboxlive.com", "title.mgt.xboxlive.com", "userpresence.xboxlive.com",
		"catalog.gamepass.com",
	})
	if insufficient.DeviceType != "Unknown" {
		t.Fatalf("insufficient Xbox traffic predicted %#v", insufficient)
	}
	console := catalog.Predict("", "192.168.1.40", []string{
		"update.xboxlive.com",
		"xccs.xboxlive.com",
		"xsts.auth.xboxlive.com",
		"catalog.gamepass.com",
		"notify.xboxlive.com",
	})
	if console.DeviceType != "Xbox" || console.Confidence != "high" {
		t.Fatalf("console-specific Xbox traffic predicted %#v", console)
	}
}

func TestCatalogRecognizesEnphaseSolarSystemFromIndependentServices(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.1.60", []string{
		"ping-udp.enphaseenergy.com",
		"reports.enphaseenergy.com",
		"provisioning.enphaseenergy.com",
		"revocations.enphase.com",
	})
	if prediction.DeviceType != "Enphase Solar System" || prediction.Confidence != "high" {
		t.Fatalf("Enphase services predicted %#v", prediction)
	}
	if prediction.DefinitionID != "enphase-solar-system" || len(prediction.Evidence) != 3 {
		t.Fatalf("Enphase evidence = %#v", prediction)
	}
}

func TestCatalogDoesNotClassifyGenericEnphaseTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{
		{"www.enphase.com"},
		{"revocations.enphase.com"},
		{"reports.enphaseenergy.com"},
	} {
		prediction := catalog.Predict("", "192.168.1.61", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("generic Enphase traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestCatalogRecognizesMideaSmartAppliance(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.1.149", []string{"module.appsmb.com"})
	if prediction.DeviceType != "Midea Smart Appliance" || prediction.Confidence != "high" {
		t.Fatalf("Midea endpoint predicted %#v", prediction)
	}
	if prediction.DefinitionID != "midea-smart-appliance" || len(prediction.Evidence) != 1 {
		t.Fatalf("Midea evidence = %#v", prediction)
	}
}

func TestCatalogDoesNotClassifyGenericMideaTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{{"www.midea.com"}, {"appsmb.com"}, {"mapp.appsmb.com"}} {
		prediction := catalog.Predict("", "192.168.1.150", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("generic Midea traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestCatalogRecognizesPetlibroSmartPetDevice(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.1.189", []string{"mqtt.us.petlibro.com"})
	if prediction.DeviceType != "Petlibro Smart Pet Device" || prediction.Confidence != "high" {
		t.Fatalf("Petlibro endpoint predicted %#v", prediction)
	}
	if prediction.DefinitionID != "petlibro-smart-pet-device" || len(prediction.Evidence) != 1 {
		t.Fatalf("Petlibro evidence = %#v", prediction)
	}
}

func TestCatalogDoesNotClassifyGenericPetlibroTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{{"petlibro.com"}, {"www.petlibro.com"}, {"support.petlibro.com"}} {
		prediction := catalog.Predict("", "192.168.1.190", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("generic Petlibro traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestCatalogRecognizesEufySecurityDevice(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"security-app.eufylife.com", "webrtc-signal-us.eufylife.com"} {
		prediction := catalog.Predict("", "192.168.1.96", []string{domain})
		if prediction.DeviceType != "Eufy Security Device" || prediction.Confidence != "high" {
			t.Fatalf("Eufy endpoint %q predicted %#v", domain, prediction)
		}
		if prediction.DefinitionID != "eufy-security-device" || len(prediction.Evidence) != 1 {
			t.Fatalf("Eufy evidence = %#v", prediction)
		}
	}
}

func TestCatalogDoesNotClassifyGenericEufyTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{{"eufylife.com"}, {"www.eufylife.com"}, {"security-mall.eufylife.com"}} {
		prediction := catalog.Predict("", "192.168.1.97", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("generic Eufy traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestCatalogRecognizesAqaraSmartHomeDevice(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.1.150", []string{"aiot-coap-usa.aqara.com"})
	if prediction.DeviceType != "Aqara Smart Home Device" || prediction.Confidence != "high" {
		t.Fatalf("Aqara endpoint predicted %#v", prediction)
	}
	if prediction.DefinitionID != "aqara-smart-home-device" || len(prediction.Evidence) != 1 {
		t.Fatalf("Aqara evidence = %#v", prediction)
	}
}

func TestCatalogDoesNotClassifyGenericAqaraTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{{"aqara.com"}, {"www.aqara.com"}, {"open-usa.aqara.com"}} {
		prediction := catalog.Predict("", "192.168.1.151", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("generic Aqara traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestCatalogRecognizesTuyaSmartHomeDeviceFromIndependentServices(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.9.81", []string{"a3.tuyaus.com", "m2.tuyaus.com"})
	if prediction.DeviceType != "Tuya Smart Home Device" || prediction.Confidence != "high" {
		t.Fatalf("Tuya endpoints predicted %#v", prediction)
	}
	if prediction.DefinitionID != "tuya-smart-home-device" || len(prediction.Evidence) != 2 {
		t.Fatalf("Tuya evidence = %#v", prediction)
	}
}

func TestCatalogDoesNotClassifySingleTuyaService(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{{"a3.tuyaus.com"}, {"m2.tuyaus.com"}, {"www.tuyaus.com"}} {
		prediction := catalog.Predict("", "192.168.9.82", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("incomplete Tuya traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestCatalogRecognizesMyQGarageDoorController(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.7.140", []string{"connect-ca.myqdevice.com"})
	if prediction.DeviceType != "MyQ Garage Door Controller" || prediction.Confidence != "high" {
		t.Fatalf("MyQ endpoint predicted %#v", prediction)
	}
	if prediction.DefinitionID != "myq-garage-door-controller" || len(prediction.Evidence) != 1 {
		t.Fatalf("MyQ evidence = %#v", prediction)
	}
}

func TestCatalogDoesNotClassifyGenericMyQTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{{"myqdevice.com"}, {"www.myqdevice.com"}, {"chamberlain.com"}} {
		prediction := catalog.Predict("", "192.168.7.141", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("generic MyQ traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestCatalogRecognizesUniFiNetworkDevice(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.7.102", []string{"ping.ui.com"})
	if prediction.DeviceType != "UniFi Network Device" || prediction.Confidence != "high" {
		t.Fatalf("UniFi endpoint predicted %#v", prediction)
	}
	if prediction.DefinitionID != "unifi-network-device" || len(prediction.Evidence) != 1 {
		t.Fatalf("UniFi evidence = %#v", prediction)
	}
}

func TestCatalogDoesNotClassifyGenericUIWebTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{{"ui.com"}, {"www.ui.com"}, {"unifi.ui.com"}} {
		prediction := catalog.Predict("", "192.168.7.103", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("generic UI traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestCatalogRecognizesTPLinkSmartHomeDevice(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.9.238", []string{"security.iot.i.tplinknbu.com", "pool.ntp.org"})
	if prediction.DeviceType != "TP-Link Smart Home Device" || prediction.Confidence != "high" {
		t.Fatalf("TP-Link endpoint predicted %#v", prediction)
	}
	if prediction.DefinitionID != "tplink-smart-home-device" || len(prediction.Evidence) != 1 {
		t.Fatalf("TP-Link evidence = %#v", prediction)
	}
}

func TestCatalogDoesNotClassifyGenericTPLinkTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{{"tplinknbu.com"}, {"www.tplink.com"}, {"app-server.iot.i.tplinknbu.com"}} {
		prediction := catalog.Predict("", "192.168.9.239", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("generic TP-Link traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestCatalogRecognizesMetaHardwareFromMultipleHardwareSignals(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.7.216", []string{
		"ar.graph.meta.com",
		"graph.facebook-hardware.com",
		"portal.fb.com",
		"fbcdn.net",
		"www.facebook.com",
	})
	if prediction.DeviceType != "Meta Hardware" || prediction.Confidence != "high" {
		t.Fatalf("Meta hardware signals predicted %#v", prediction)
	}
	if prediction.DefinitionID != "meta-hardware" || len(prediction.Evidence) != 3 {
		t.Fatalf("Meta hardware evidence = %#v", prediction)
	}
}

func TestCatalogDoesNotClassifyGenericMetaTrafficAsHardware(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{
		{"www.facebook.com", "fbcdn.net"},
		{"ar.graph.meta.com"},
		{"graph.facebook-hardware.com"},
	} {
		prediction := catalog.Predict("", "192.168.7.217", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("incomplete Meta traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestCatalogRecognizesNeakasaSmartPetDevice(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.9.221", []string{
		"devus.neakasa.com",
		"public.iot-as-mqtt.us-east-1.aliyuncs.com",
	})
	if prediction.DeviceType != "Neakasa Smart Pet Device" || prediction.Confidence != "high" {
		t.Fatalf("Neakasa endpoint predicted %#v", prediction)
	}
	if prediction.DefinitionID != "neakasa-smart-pet-device" || len(prediction.Evidence) != 1 {
		t.Fatalf("Neakasa evidence = %#v", prediction)
	}
}

func TestCatalogDoesNotClassifyGenericNeakasaTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{{"neakasa.com"}, {"www.neakasa.com"}, {"support.neakasa.com"}} {
		prediction := catalog.Predict("", "192.168.9.222", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("generic Neakasa traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestCatalogRecognizesAmazonAlexaDeviceFromIndependentServices(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.7.82", []string{
		"api.amazonalexa.com",
		"unagi-na.amazon.com",
		"msh.amazon.com",
		"mmechocaptiveportal.com",
	})
	if prediction.DeviceType != "Amazon Alexa Device" || prediction.Confidence != "high" {
		t.Fatalf("Alexa services predicted %#v", prediction)
	}
	if prediction.DefinitionID != "amazon-alexa-device" || len(prediction.Evidence) != 4 {
		t.Fatalf("Alexa evidence = %#v", prediction)
	}
}

func TestCatalogDoesNotClassifyIncompleteAlexaTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{
		{"api.amazonalexa.com"},
		{"unagi-na.amazon.com"},
		{"dcape-na.amazon.com"},
		{"www.amazon.com", "images-na.ssl-images-amazon.com"},
	} {
		prediction := catalog.Predict("", "192.168.7.83", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("incomplete Alexa traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestCatalogRecognizesOlderAmazonEchoTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.7.19", []string{
		"api.amazonalexa.com",
		"dcape-na.amazon.com",
		"d3p8zr0ffa9t17.cloudfront.net",
		"ap-guc3.spotify.com",
	})
	if prediction.DeviceType != "Amazon Alexa Device" || prediction.Confidence != "high" {
		t.Fatalf("older Echo services predicted %#v", prediction)
	}
	if prediction.DefinitionID != "amazon-alexa-device" || len(prediction.Evidence) != 2 {
		t.Fatalf("older Echo evidence = %#v", prediction)
	}
}

func TestCatalogRecognizesPhilipsHueBridgeFromIndependentServices(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prediction := catalog.Predict("", "192.168.7.39", []string{
		"diag.meethue.com",
		"data.meethue.com",
		"prod.huedatastore.com",
		"mqtt-eu-03.iot.meethue.com",
	})
	if prediction.DeviceType != "Philips Hue Bridge" || prediction.Confidence != "high" {
		t.Fatalf("Hue bridge services predicted %#v", prediction)
	}
	if prediction.DefinitionID != "philips-hue-bridge" || len(prediction.Evidence) != 4 {
		t.Fatalf("Hue bridge evidence = %#v", prediction)
	}
}

func TestCatalogDoesNotClassifyIncompleteHueTraffic(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, domains := range [][]string{
		{"diag.meethue.com"},
		{"prod.huedatastore.com"},
		{"www.meethue.com", "www.philips-hue.com"},
	} {
		prediction := catalog.Predict("", "192.168.7.40", domains)
		if prediction.DeviceType != "Unknown" {
			t.Fatalf("incomplete Hue traffic %v predicted %#v", domains, prediction)
		}
	}
}

func TestManagerLoadsVersionedExternalCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device-catalog.json")
	data, err := catalogFiles.ReadFile(embeddedCatalogName)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), `"catalog_version": "2026.07.17"`, `"catalog_version": "custom.1"`, 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(path)
	manager.checkInterval = 0
	if info := manager.Info(); info.Source != "external" || info.CatalogVersion != "custom.1" {
		t.Fatalf("external catalog info = %#v", info)
	}

	invalid := strings.Replace(updated, `"schema_version": 1`, `"schema_version": 99`, 1)
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	info := manager.Info()
	if info.CatalogVersion != "custom.1" || info.LastError == "" {
		t.Fatalf("manager did not keep last known good catalog: %#v", info)
	}
}

func TestValidateRejectsDuplicateDefinitionIDs(t *testing.T) {
	catalog, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	catalog.Definitions = append(catalog.Definitions, catalog.Definitions[0])
	if err := Validate(catalog); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("Validate error = %v", err)
	}
}

func TestParseRejectsTrailingJSON(t *testing.T) {
	data, err := catalogFiles.ReadFile(embeddedCatalogName)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(`{"unexpected":true}`)...)
	if _, err := Parse(data); err == nil {
		t.Fatal("Parse accepted a second JSON value")
	}
}
