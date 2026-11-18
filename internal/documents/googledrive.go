package documents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// googleDrivePathPrefix marks a Document.FilePath as living on Google Drive rather than local
// disk. New uploads always produce this form ("googledrive:{fileId}"); it is also the exact string
// the old Java GoogleDriveFileStorageService wrote into arquivo_path, carried across verbatim by
// the ETL, so reads must keep accepting it.
const googleDrivePathPrefix = "googledrive:"

// driveRootFolderName is the app-owned folder created in each tenant's own Drive, with one
// subfolder per Origin underneath (mirroring LocalStorage's subfolderFor layout). The app can only
// see folders it created because the OAuth scope is drive.file.
const driveRootFolderName = "Atlas"

const (
	driveFilesURL   = "https://www.googleapis.com/drive/v3/files"
	driveUploadURL  = "https://www.googleapis.com/upload/drive/v3/files"
	driveFolderMime = "application/vnd.google-apps.folder"
)

// IntegrationsLookup is the one capability this package needs from internal/integrations — a
// narrow interface (rather than importing *integrations.Service directly) so this package doesn't
// need to know anything else about that module.
type IntegrationsLookup interface {
	GoogleDriveRefreshToken(ctx context.Context) (string, error)
}

// GoogleDriveClient reads and writes a tenant's files on Google Drive using that tenant's own
// drive.file-scoped credential (resolved per call from the request's tenant schema). Reads cover
// both files migrated from the old Java storage and files this backend uploaded; writes create an
// app-owned "Atlas/{Origem}" folder tree and upload files under it.
type GoogleDriveClient struct {
	oauthConfig  *oauth2.Config
	integrations IntegrationsLookup
}

func NewGoogleDriveClient(clientID, clientSecret string, integrations IntegrationsLookup) *GoogleDriveClient {
	return &GoogleDriveClient{
		oauthConfig:  &oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, Endpoint: google.Endpoint},
		integrations: integrations,
	}
}

// DriveStorage is the file backend documents.Service persists to. *GoogleDriveClient is the
// production implementation; tests supply an in-memory fake.
type DriveStorage interface {
	FetchFile(ctx context.Context, path string) ([]byte, error)
	UploadFile(ctx context.Context, origin Origin, fileType FileType, filename string, content []byte) (string, error)
	DeleteFile(ctx context.Context, path string) error
}

// errDriveAuth marks a Drive failure the user fixes by reconnecting the account (refresh token
// expired or revoked, access withdrawn) rather than a server fault.
var errDriveAuth = errors.New("google drive authorization is no longer valid")

// isDriveAuthError reports whether err means the tenant's Drive connection must be renewed.
func isDriveAuthError(err error) bool {
	var retrieve *oauth2.RetrieveError
	return errors.Is(err, errDriveAuth) || errors.As(err, &retrieve)
}

// driveStatusError tags 401/403 answers as errDriveAuth, keeping the detailed error.
func driveStatusError(status int, err error) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("%w: %w", errDriveAuth, err)
	}
	return err
}

// IsGoogleDrivePath reports whether path is a "googledrive:{fileId}" reference rather than a local
// filesystem path.
func IsGoogleDrivePath(path string) bool {
	return strings.HasPrefix(path, googleDrivePathPrefix)
}

// DrivePath builds the FilePath value persisted for a file uploaded to Drive.
func DrivePath(fileID string) string {
	return googleDrivePathPrefix + fileID
}

func driveFileID(path string) string {
	return strings.TrimPrefix(path, googleDrivePathPrefix)
}

// client mints an authenticated Drive client from the tenant's stored refresh token. The error is
// returned unwrapped from integrations so callers can detect apperr.NotFound ("not connected") and
// turn it into a user-facing message.
func (c *GoogleDriveClient) client(ctx context.Context) (*http.Client, error) {
	refreshToken, err := c.integrations.GoogleDriveRefreshToken(ctx)
	if err != nil {
		return nil, err
	}
	tokenSource := c.oauthConfig.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	return oauth2.NewClient(ctx, tokenSource), nil
}

// FetchFile downloads a file's raw bytes from Google Drive by file ID.
func (c *GoogleDriveClient) FetchFile(ctx context.Context, path string) ([]byte, error) {
	client, err := c.client(ctx)
	if err != nil {
		return nil, fmt.Errorf("documents: getting google drive credential: %w", err)
	}
	u := fmt.Sprintf("%s/%s?alt=media", driveFilesURL, url.PathEscape(driveFileID(path)))
	resp, err := client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("documents: fetching file from google drive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("documents: google drive returned status %d fetching %s: %s", resp.StatusCode, path, body)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("documents: reading google drive response body: %w", err)
	}
	return data, nil
}

// UploadFile writes content to the tenant's Drive under Atlas/{subpasta-da-origem} and returns the
// created file's ID (persist it with DrivePath). A missing Drive connection surfaces as the
// integrations error (apperr.NotFound) so the caller can ask the user to connect.
func (c *GoogleDriveClient) UploadFile(ctx context.Context, origin Origin, fileType FileType, filename string, content []byte) (string, error) {
	client, err := c.client(ctx)
	if err != nil {
		return "", err
	}
	rootID, err := c.ensureFolder(ctx, client, driveRootFolderName, "root")
	if err != nil {
		return "", err
	}
	folderID, err := c.ensureFolder(ctx, client, subfolderFor(origin), rootID)
	if err != nil {
		return "", err
	}
	return c.createFile(ctx, client, folderID, safeDriveName(filename, fileType), uploadContentType(fileType), content)
}

// DeleteFile removes a Drive file by its FilePath ("googledrive:{id}"). A file that no longer
// exists is a silent no-op, matching LocalStorage.Delete's idempotence.
func (c *GoogleDriveClient) DeleteFile(ctx context.Context, path string) error {
	client, err := c.client(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		fmt.Sprintf("%s/%s", driveFilesURL, url.PathEscape(driveFileID(path))), nil)
	if err != nil {
		return fmt.Errorf("documents: building google drive delete request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("documents: deleting file from google drive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("documents: google drive returned status %d deleting %s: %s", resp.StatusCode, path, body)
	}
	return nil
}

// ensureFolder returns the ID of the folder named name under parent, creating it if absent.
// Best-effort: a concurrent create race may leave a duplicate folder, which is harmless.
func (c *GoogleDriveClient) ensureFolder(ctx context.Context, client *http.Client, name, parent string) (string, error) {
	q := fmt.Sprintf("name = '%s' and mimeType = '%s' and '%s' in parents and trashed = false",
		escapeDriveQuery(name), driveFolderMime, escapeDriveQuery(parent))
	u := driveFilesURL + "?spaces=drive&fields=files(id)&q=" + url.QueryEscape(q)
	resp, err := client.Get(u)
	if err != nil {
		return "", fmt.Errorf("documents: searching google drive folder %q: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", driveStatusError(resp.StatusCode, fmt.Errorf("documents: google drive folder search returned status %d: %s", resp.StatusCode, body))
	}
	var found struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&found); err != nil {
		return "", fmt.Errorf("documents: decoding google drive folder search: %w", err)
	}
	if len(found.Files) > 0 {
		return found.Files[0].ID, nil
	}

	meta, err := json.Marshal(map[string]any{
		"name":     name,
		"mimeType": driveFolderMime,
		"parents":  []string{parent},
	})
	if err != nil {
		return "", fmt.Errorf("documents: encoding google drive folder metadata: %w", err)
	}
	resp2, err := client.Post(driveFilesURL+"?fields=id", "application/json", bytes.NewReader(meta))
	if err != nil {
		return "", fmt.Errorf("documents: creating google drive folder %q: %w", name, err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp2.Body, 1024))
		return "", driveStatusError(resp2.StatusCode, fmt.Errorf("documents: google drive folder create returned status %d: %s", resp2.StatusCode, body))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&created); err != nil {
		return "", fmt.Errorf("documents: decoding google drive folder create: %w", err)
	}
	if created.ID == "" {
		return "", fmt.Errorf("documents: google drive folder create returned no id")
	}
	return created.ID, nil
}

// createFile uploads content as a new file in folderID using a multipart/related request, which
// carries the JSON metadata and the media bytes in one call.
func (c *GoogleDriveClient) createFile(ctx context.Context, client *http.Client, folderID, name, mediaType string, content []byte) (string, error) {
	meta, err := json.Marshal(map[string]any{
		"name":    name,
		"parents": []string{folderID},
	})
	if err != nil {
		return "", fmt.Errorf("documents: encoding google drive file metadata: %w", err)
	}
	boundary, err := randomHexName()
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Type: application/json; charset=UTF-8\r\n\r\n")
	body.Write(meta)
	body.WriteString("\r\n--" + boundary + "\r\n")
	body.WriteString("Content-Type: " + mediaType + "\r\n\r\n")
	body.Write(content)
	body.WriteString("\r\n--" + boundary + "--\r\n")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, driveUploadURL+"?uploadType=multipart&fields=id", bytes.NewReader(body.Bytes()))
	if err != nil {
		return "", fmt.Errorf("documents: building google drive upload request: %w", err)
	}
	req.Header.Set("Content-Type", "multipart/related; boundary="+boundary)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("documents: uploading file to google drive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", driveStatusError(resp.StatusCode, fmt.Errorf("documents: google drive upload returned status %d: %s", resp.StatusCode, respBody))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return "", fmt.Errorf("documents: decoding google drive upload: %w", err)
	}
	if created.ID == "" {
		return "", fmt.Errorf("documents: google drive upload returned no id")
	}
	return created.ID, nil
}

// uploadContentType maps a FileType to the media type sent with the Drive upload.
func uploadContentType(fileType FileType) string {
	if ct := contentTypeByFileType[fileType]; ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// safeDriveName keeps the user's original filename for display in their own Drive, stripping the
// path separators that would be meaningless there and falling back to a generic name.
func safeDriveName(filename string, fileType FileType) string {
	name := strings.TrimSpace(filename)
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	if name == "" {
		name = "documento." + extensionFor(fileType)
	}
	return name
}

// escapeDriveQuery escapes a value interpolated into a Drive `q` expression.
func escapeDriveQuery(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}
