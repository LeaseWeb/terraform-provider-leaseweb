package leaseweb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	leasewebAPIURL   string
	leasewebAPIToken string
	leasewebClient   *http.Client
)

// Server -
type Server struct {
	ID       string
	Contract struct {
		Reference string
	}
	NetworkInterfaces struct {
		Public struct {
			IP string
		}
		RemoteManagement struct {
			IP string
		}
	}
	Location struct {
		Site  string
		Suite string
		Rack  string
		Unit  string
	}
}

// IP -
type IP struct {
	IP            string
	ReverseLookup string
	NullRouted    bool
}

// DHCPLease -
type DHCPLease struct {
	Leases []struct {
		IP       string
		Bootfile string
	}
}

// GetBootfile -
func (l *DHCPLease) GetBootfile() string {
	if len(l.Leases) == 0 {
		return ""
	}
	return l.Leases[0].Bootfile
}

// PowerInfo -
type PowerInfo struct {
	IPMI struct {
		Status string
	}
	PDU struct {
		Status string
	}
}

// IsPoweredOn -
func (p *PowerInfo) IsPoweredOn() bool {
	return p.PDU.Status != "off" && p.IPMI.Status != "off"
}

// NetworkInterfaceInfo -
type NetworkInterfaceInfo struct {
	Status string
}

// IsOpened -
func (n *NetworkInterfaceInfo) IsOpened() bool {
	return n.Status == "OPEN"
}

// NotificationSetting -
type NotificationSetting struct {
	ID        string  `json:"id,omitempty"`
	Frequency string  `json:"frequency"`
	Threshold float64 `json:"threshold,string"`
	Unit      string  `json:"unit"`
}

// Credential -
type Credential struct {
	Type     string `json:"type"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// OperatingSystem -
type OperatingSystem struct {
	ID   string
	Name string
}

// ControlPanel -
type ControlPanel struct {
	ID   string
	Name string
}

// Payload -
type Payload map[string]interface{}

// Job -
type Job struct {
	UUID    string
	Status  string
	Payload Payload
}

// ErrorInfo -
type ErrorInfo struct {
	Context       string
	CorrelationID string              `json:"correlationId"`
	Code          string              `json:"errorCode"`
	Message       string              `json:"errorMessage"`
	Details       map[string][]string `json:"errorDetails"`
}

func (erri *ErrorInfo) Error() string {
	return "(" + erri.Code + ") " + erri.Context + ": " + erri.Message
}

// DecodingError -
type DecodingError struct {
	Context string
	Message string
}

func (errd *DecodingError) Error() string {
	return errd.Context + ": error while decoding JSON response body (" + errd.Message + ")"
}

// NewDecodingError -
func NewDecodingError(ctx string, err error) *DecodingError {
	return &DecodingError{Context: ctx, Message: err.Error()}
}

// EncodingError -
type EncodingError struct {
	Context string
	Message string
}

func (erre *EncodingError) Error() string {
	return erre.Context + ": error while encoding JSON request body (" + erre.Message + ")"
}

// NewEncodingError -
func NewEncodingError(ctx string, err error) *EncodingError {
	return &EncodingError{Context: ctx, Message: err.Error()}
}

func parseErrorInfo(r io.Reader, ctx string) error {
	erri := ErrorInfo{Context: ctx}

	if err := json.NewDecoder(r).Decode(&erri); err != nil {
		return NewDecodingError(ctx, err)
	}

	return &erri
}

func doAPIRequest(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Lsw-Auth", leasewebAPIToken)

	if method == http.MethodPost || method == http.MethodPut {
		// not always needed even for those methods but this is simpler for now
		request.Header.Set("Content-Type", "application/json")
	}

	tflog.Trace(ctx, "executing API request", map[string]interface{}{
		"url":    url,
		"method": method,
	})

	response, err := leasewebClient.Do(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func logAPIError(ctx context.Context, method, url string, err error) {
	fields := map[string]interface{}{
		"url":    url,
		"method": method,
	}

	if erri, ok := err.(*ErrorInfo); ok {
		fields["context"] = erri.Context
		fields["code"] = erri.Code
		fields["message"] = erri.Message
		fields["correlation_id"] = erri.CorrelationID

		if len(erri.Details) != 0 {
			for field, details := range erri.Details {
				fields["detail_"+field] = details
			}
		}
	} else {
		fields["message"] = err.Error()
	}

	tflog.Error(ctx, "API request error", fields)
}

func getServer(ctx context.Context, serverID string) (*Server, error) {
	apiCtx := fmt.Sprintf("getting server %s", serverID)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s", leasewebAPIURL, serverID)
	method := http.MethodGet

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var server Server
	err = json.NewDecoder(response.Body).Decode(&server)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	server.NetworkInterfaces.Public.IP = strings.SplitN(server.NetworkInterfaces.Public.IP, "/", 2)[0]
	server.NetworkInterfaces.RemoteManagement.IP = strings.SplitN(server.NetworkInterfaces.RemoteManagement.IP, "/", 2)[0]

	return &server, nil
}

func getServerIP(ctx context.Context, serverID string, ip string) (*IP, error) {
	apiCtx := fmt.Sprintf("getting server %s IP %s", serverID, ip)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/ips/%s", leasewebAPIURL, serverID, ip)
	method := http.MethodGet

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var ipData IP
	err = json.NewDecoder(response.Body).Decode(&ipData)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &ipData, nil
}

func getServerLease(ctx context.Context, serverID string) (*DHCPLease, error) {
	apiCtx := fmt.Sprintf("getting server %s lease", serverID)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/leases", leasewebAPIURL, serverID)
	method := http.MethodGet

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var dhcpLease DHCPLease
	err = json.NewDecoder(response.Body).Decode(&dhcpLease)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &dhcpLease, nil
}

func getPowerInfo(ctx context.Context, serverID string) (*PowerInfo, error) {
	apiCtx := fmt.Sprintf("getting server %s power info", serverID)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/powerInfo", leasewebAPIURL, serverID)
	method := http.MethodGet

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var powerInfo PowerInfo
	err = json.NewDecoder(response.Body).Decode(&powerInfo)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &powerInfo, nil
}

func getNetworkInterfaceInfo(ctx context.Context, serverID string, networkType string) (*NetworkInterfaceInfo, error) {
	apiCtx := fmt.Sprintf("getting server network interface info")
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/networkInterfaces/%s", leasewebAPIURL, serverID, networkType)
	method := http.MethodGet

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var networkInterfaceInfo NetworkInterfaceInfo
	err = json.NewDecoder(response.Body).Decode(&networkInterfaceInfo)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &networkInterfaceInfo, nil
}

func updateReference(ctx context.Context, serverID string, reference string) error {
	apiCtx := fmt.Sprintf("updating server %s reference", serverID)

	requestBody := new(bytes.Buffer)
	err := json.NewEncoder(requestBody).Encode(struct {
		Reference string `json:"reference"`
	}{
		Reference: reference,
	})
	if err != nil {
		return NewEncodingError(apiCtx, err)
	}

	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s", leasewebAPIURL, serverID)
	method := http.MethodPut

	response, err := doAPIRequest(ctx, method, url, requestBody)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return err
	}

	return nil
}

func updateReverseLookup(ctx context.Context, serverID string, ip string, reverseLookup string) error {
	apiCtx := fmt.Sprintf("updating server %s reverse lookup for IP %s", serverID, ip)

	requestBody := new(bytes.Buffer)
	err := json.NewEncoder(requestBody).Encode(struct {
		ReverseLookup string `json:"reverseLookup"`
	}{
		ReverseLookup: reverseLookup,
	})
	if err != nil {
		return NewEncodingError(apiCtx, err)
	}

	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/ips/%s", leasewebAPIURL, serverID, ip)
	method := http.MethodPut

	response, err := doAPIRequest(ctx, method, url, requestBody)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return err
	}

	return nil
}

func powerOnServer(ctx context.Context, serverID string) error {
	apiCtx := fmt.Sprintf("powering on server %s", serverID)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/powerOn", leasewebAPIURL, serverID)
	method := http.MethodPost

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusAccepted {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return err
	}

	return nil
}

func powerOffServer(ctx context.Context, serverID string) error {
	apiCtx := fmt.Sprintf("powering off server %s", serverID)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/powerOff", leasewebAPIURL, serverID)
	method := http.MethodPost

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusAccepted {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return err
	}

	return nil
}

func addDHCPLease(ctx context.Context, serverID string, bootfile string) error {
	apiCtx := fmt.Sprintf("adding server %s lease", serverID)

	requestBody := new(bytes.Buffer)
	err := json.NewEncoder(requestBody).Encode(struct {
		Bootfile string `json:"bootfile"`
	}{
		Bootfile: bootfile,
	})
	if err != nil {
		return NewEncodingError(apiCtx, err)
	}

	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/leases", leasewebAPIURL, serverID)
	method := http.MethodPost

	response, err := doAPIRequest(ctx, method, url, requestBody)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return err
	}

	return nil
}

func removeDHCPLease(ctx context.Context, serverID string) error {
	apiCtx := fmt.Sprintf("removing server %s lease", serverID)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/leases", leasewebAPIURL, serverID)
	method := http.MethodDelete

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return err
	}

	return nil
}

func openNetworkInterface(ctx context.Context, serverID string, networkType string) error {
	apiCtx := fmt.Sprintf("opening server %s network interface %s", serverID, networkType)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/networkInterfaces/%s/open", leasewebAPIURL, serverID, networkType)
	method := http.MethodPost

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return err
	}

	return nil
}

func closeNetworkInterface(ctx context.Context, serverID string, networkType string) error {
	apiCtx := fmt.Sprintf("closing server %s network interface %s", serverID, networkType)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/networkInterfaces/%s/close", leasewebAPIURL, serverID, networkType)
	method := http.MethodPost

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return err
	}

	return nil
}

func nullIP(ctx context.Context, serverID string, ip string) error {
	apiCtx := fmt.Sprintf("nulling server %s IP %s", serverID, ip)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/ips/%s/null", leasewebAPIURL, serverID, ip)
	method := http.MethodPost

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusAccepted {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return err
	}

	return nil
}

func unnullIP(ctx context.Context, serverID string, ip string) error {
	apiCtx := fmt.Sprintf("unnulling server %s IP %s", serverID, ip)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/ips/%s/unnull", leasewebAPIURL, serverID, ip)
	method := http.MethodPost

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusAccepted {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return err
	}

	return nil
}

func createDedicatedServerNotificationSetting(ctx context.Context, serverID string, notificationType string, notificationSetting *NotificationSetting) (*NotificationSetting, error) {
	apiCtx := fmt.Sprintf("creating server %s notification setting %s", serverID, notificationType)

	requestBody := new(bytes.Buffer)
	err := json.NewEncoder(requestBody).Encode(notificationSetting)
	if err != nil {
		return nil, NewEncodingError(apiCtx, err)
	}

	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/notificationSettings/%s", leasewebAPIURL, serverID, notificationType)
	method := http.MethodPost

	response, err := doAPIRequest(ctx, method, url, requestBody)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusCreated {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var createdNotificationSetting NotificationSetting
	err = json.NewDecoder(response.Body).Decode(&createdNotificationSetting)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &createdNotificationSetting, nil
}

func getDedicatedServerNotificationSetting(ctx context.Context, serverID string, notificationType string, notificationSettingID string) (*NotificationSetting, error) {
	apiCtx := fmt.Sprintf("getting server %s notification setting %s", serverID, notificationType)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/notificationSettings/%s/%s", leasewebAPIURL, serverID, notificationType, notificationSettingID)
	method := http.MethodGet

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var notificationSetting NotificationSetting
	err = json.NewDecoder(response.Body).Decode(&notificationSetting)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &notificationSetting, nil
}

func updateDedicatedServerNotificationSetting(ctx context.Context, serverID string, notificationType string, notificationSettingID string, notificationSetting *NotificationSetting) (*NotificationSetting, error) {
	apiCtx := fmt.Sprintf("updating server %s notification setting %s", serverID, notificationType)

	requestBody := new(bytes.Buffer)
	err := json.NewEncoder(requestBody).Encode(notificationSetting)
	if err != nil {
		return nil, NewEncodingError(apiCtx, err)
	}

	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/notificationSettings/%s/%s", leasewebAPIURL, serverID, notificationType, notificationSettingID)
	method := http.MethodPut

	response, err := doAPIRequest(ctx, method, url, requestBody)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var updatedNotificationSetting NotificationSetting
	err = json.NewDecoder(response.Body).Decode(&updatedNotificationSetting)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &updatedNotificationSetting, nil
}

func deleteDedicatedServerNotificationSetting(ctx context.Context, serverID string, notificationType string, notificationSettingID string) error {
	apiCtx := fmt.Sprintf("deleting server %s notification setting %s", serverID, notificationType)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/notificationSettings/%s/%s", leasewebAPIURL, serverID, notificationType, notificationSettingID)
	method := http.MethodDelete

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return err
	}

	return nil
}

func createDedicatedServerCredential(ctx context.Context, serverID string, credential *Credential) (*Credential, error) {
	apiCtx := fmt.Sprintf("creating server %s credential %s", serverID, credential.Type)

	requestBody := new(bytes.Buffer)
	err := json.NewEncoder(requestBody).Encode(credential)
	if err != nil {
		return nil, NewEncodingError(apiCtx, err)
	}

	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/credentials", leasewebAPIURL, serverID)
	method := http.MethodPost

	response, err := doAPIRequest(ctx, method, url, requestBody)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var createdCredential Credential
	err = json.NewDecoder(response.Body).Decode(&createdCredential)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &createdCredential, nil
}

func getDedicatedServerCredential(ctx context.Context, serverID string, credentialType string, username string) (*Credential, error) {
	apiCtx := fmt.Sprintf("getting server %s credential %s", serverID, credentialType)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/credentials/%s/%s", leasewebAPIURL, serverID, credentialType, username)
	method := http.MethodGet

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var credential Credential
	err = json.NewDecoder(response.Body).Decode(&credential)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &credential, nil
}

func updateDedicatedServerCredential(ctx context.Context, serverID string, credential *Credential) (*Credential, error) {
	apiCtx := fmt.Sprintf("updating server %s credential %s", serverID, credential.Type)

	requestBody := new(bytes.Buffer)
	err := json.NewEncoder(requestBody).Encode(struct {
		Password string `json:"password"`
	}{
		Password: credential.Password,
	})
	if err != nil {
		return nil, NewEncodingError(apiCtx, err)
	}

	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/credentials/%s/%s", leasewebAPIURL, serverID, credential.Type, credential.Username)
	method := http.MethodPut

	response, err := doAPIRequest(ctx, method, url, requestBody)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var updatedCredential Credential
	err = json.NewDecoder(response.Body).Decode(&updatedCredential)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &updatedCredential, nil
}

func deleteDedicatedServerCredential(ctx context.Context, serverID string, credential *Credential) error {
	apiCtx := fmt.Sprintf("deleting server %s credential %s", serverID, credential.Type)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/credentials/%s/%s", leasewebAPIURL, serverID, credential.Type, credential.Username)
	method := http.MethodDelete

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return err
	}

	return nil
}

func getOperatingSystems(ctx context.Context) ([]OperatingSystem, error) {
	apiCtx := fmt.Sprintf("getting operating systems")
	url := fmt.Sprintf("%s/bareMetals/v2/operatingSystems", leasewebAPIURL)
	method := http.MethodGet

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var operatingSystems struct {
		OperatingSystems []OperatingSystem
	}

	err = json.NewDecoder(response.Body).Decode(&operatingSystems)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	// to be exact we would need to support pagination by checking the metadata and make multiple requests if needed
	// but with the default offset and limit values we already get the full list at the moment

	return operatingSystems.OperatingSystems, nil
}

func getControlPanels(ctx context.Context, operatingSystemID string) ([]ControlPanel, error) {
	apiCtx := fmt.Sprintf("getting control panels")

	u, err := url.Parse(fmt.Sprintf("%s/bareMetals/v2/controlPanels", leasewebAPIURL))
	if err != nil {
		return nil, err
	}

	if operatingSystemID != "" {
		v := url.Values{}
		v.Set("operatingSystemId", operatingSystemID)
		u.RawQuery = v.Encode()
	}

	url := u.String()
	method := http.MethodGet

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var controlPanels struct {
		ControlPanels []ControlPanel
	}

	err = json.NewDecoder(response.Body).Decode(&controlPanels)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return controlPanels.ControlPanels, nil
}

func launchInstallationJob(ctx context.Context, serverID string, payload *Payload) (*Job, error) {
	apiCtx := fmt.Sprintf("launching installation job for server %s", serverID)

	requestBody := new(bytes.Buffer)
	err := json.NewEncoder(requestBody).Encode(payload)
	if err != nil {
		return nil, NewEncodingError(apiCtx, err)
	}

	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/install", leasewebAPIURL, serverID)
	method := http.MethodPost

	response, err := doAPIRequest(ctx, method, url, requestBody)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusAccepted {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var installationJob Job

	err = json.NewDecoder(response.Body).Decode(&installationJob)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &installationJob, nil
}

func getLatestInstallationJob(ctx context.Context, serverID string) (*Job, error) {
	apiCtx := fmt.Sprintf("getting latest installation job for server %s", serverID)

	u, err := url.Parse(fmt.Sprintf("%s/bareMetals/v2/servers/%s/jobs", leasewebAPIURL, serverID))
	if err != nil {
		return nil, err
	}

	v := url.Values{}
	v.Set("type", "install")
	u.RawQuery = v.Encode()

	url := u.String()
	method := http.MethodGet

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var jobs struct {
		Jobs []Job
	}

	err = json.NewDecoder(response.Body).Decode(&jobs)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &jobs.Jobs[0], nil
}

func getJob(ctx context.Context, serverID string, jobUUID string) (*Job, error) {
	apiCtx := fmt.Sprintf("getting job status for server %s", serverID)
	url := fmt.Sprintf("%s/bareMetals/v2/servers/%s/jobs/%s", leasewebAPIURL, serverID, jobUUID)
	method := http.MethodGet

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var job Job

	err = json.NewDecoder(response.Body).Decode(&job)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return &job, nil
}

func getServersBatch(ctx context.Context, offset int, limit int, site string) ([]Server, error) {
	apiCtx := fmt.Sprintf("getting servers list")

	u, err := url.Parse(fmt.Sprintf("%s/bareMetals/v2/servers", leasewebAPIURL))
	if err != nil {
		return nil, err
	}

	v := url.Values{}

	if offset >= 0 {
		v.Set("offset", strconv.Itoa(offset))
	}

	if limit >= 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	if site != "" {
		v.Set("site", site)
	}

	u.RawQuery = v.Encode()

	url := u.String()
	method := http.MethodGet

	response, err := doAPIRequest(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err := parseErrorInfo(response.Body, apiCtx)
		logAPIError(ctx, method, url, err)
		return nil, err
	}

	var serverList struct {
		Servers []Server
	}

	err = json.NewDecoder(response.Body).Decode(&serverList)
	if err != nil {
		return nil, NewDecodingError(apiCtx, err)
	}

	return serverList.Servers, nil
}

func getAllServers(ctx context.Context, site string) ([]Server, error) {
	var allServers []Server
	offset := 0
	limit := 20

	for {
		serversBatch, err := getServersBatch(ctx, offset, limit, site)
		if err != nil {
			return nil, err
		}

		if len(serversBatch) == 0 {
			break
		}

		allServers = append(allServers, serversBatch...)
		offset += limit
	}

	return allServers, nil
}
