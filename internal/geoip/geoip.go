package geoip

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
)

type Client struct {
	httpClient *http.Client
}

func NewClient(httpClient *http.Client) *Client {
	return &Client{
		httpClient: httpClient,
	}
}

type GeoIPResponse struct {
	Status     string  `json:"status"`
	Message    string  `json:"message"`
	Country    string  `json:"country"`
	RegionName string  `json:"regionName"`
	City       string  `json:"city"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	ISP        string  `json:"isp"`
}

var ErrUnsupportedIpAddress = fmt.Errorf("unsupported ip address")

func (c *Client) GeoIP(ctx context.Context, ipAddr net.IP) (*GeoIPResponse, error) {
	if ipAddr.IsPrivate() || ipAddr.IsLoopback() || ipAddr.IsUnspecified() {
		return nil, ErrUnsupportedIpAddress
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://ip-api.com/json/%s", ipAddr), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer res.Body.Close()

	resBody := &GeoIPResponse{}
	if err := json.NewDecoder(res.Body).Decode(resBody); err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resBody.Status != "success" {
		return nil, fmt.Errorf("geo api error: %s", resBody.Message)
	}

	return resBody, nil
}
