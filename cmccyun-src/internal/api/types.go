package api

// File is the normalised view of one entry returned by the cloud drive.
type File struct {
	ID       string
	Name     string
	Size     int64
	IsDir    bool
	Created  string
	Modified string
	// DownloadURL is only populated for non-directory entries and is refreshed
	// on demand because the CDN link carries a short-lived signature.
	DownloadURL string
}

// baseResp is the common envelope used by every 139 API response.
type baseResp struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// listResp is the response of POST /file/list.
type listResp struct {
	baseResp
	Data struct {
		Items []struct {
			FileId     string `json:"fileId"`
			Name       string `json:"name"`
			Size       int64  `json:"size"`
			Type       string `json:"type"`
			CreatedAt  string `json:"createdAt"`
			UpdatedAt  string `json:"updatedAt"`
			Thumbnails []struct {
				Style string `json:"style"`
				Url   string `json:"url"`
			} `json:"thumbnailUrls"`
		} `json:"items"`
		NextPageCursor string `json:"nextPageCursor"`
	} `json:"data"`
}

type partInfo struct {
	PartNumber int    `json:"partNumber"`
	PartSize   int64  `json:"partSize,omitempty"`
	UploadUrl  string `json:"uploadUrl,omitempty"`
}

// createResp is the response of POST /file/create (both file and folder mode).
type createResp struct {
	baseResp
	Data struct {
		FileId      string     `json:"fileId"`
		FileName    string     `json:"fileName"`
		UploadId    string     `json:"uploadId"`
		PartInfos   []partInfo `json:"partInfos"`
		Exist       bool       `json:"exist"`
		RapidUpload bool       `json:"rapidUpload"`
	} `json:"data"`
}

// uploadURLResp is the response of POST /file/getUploadUrl.
type uploadURLResp struct {
	baseResp
	Data struct {
		FileId    string     `json:"fileId"`
		UploadId  string     `json:"uploadId"`
		PartInfos []partInfo `json:"partInfos"`
	} `json:"data"`
}

// routePolicyResp is the response of POST /user/route/qryRoutePolicy.
type routePolicyResp struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    struct {
		RoutePolicyList []struct {
			SiteID      string `json:"siteID"`
			SiteCode    string `json:"siteCode"`
			ModName     string `json:"modName"`
			HttpUrl     string `json:"httpUrl"`
			HttpsUrl    string `json:"httpsUrl"`
			EnvID       string `json:"envID"`
			ModAddrType int    `json:"modAddrType"`
		} `json:"routePolicyList"`
	} `json:"data"`
}

// downloadResp is the response of POST /file/getDownloadUrl.
type downloadResp struct {
	baseResp
	Data struct {
		CdnUrl string `json:"cdnUrl"`
		Url    string `json:"url"`
	} `json:"data"`
}

// spaceResp is the response of the personal-cloud storage query.
type spaceResp struct {
	baseResp
	Data struct {
		DiskSize     int64 `json:"diskSize"`
		FreeDiskSize int64 `json:"freeDiskSize"`
	} `json:"data"`
}
