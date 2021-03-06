package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DAddYE/vips"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/julienschmidt/httprouter"
	"github.com/spf13/viper"
	"golang.org/x/crypto/sha3"
)
/*
"os"
*/

var (
	port       int
	bucket     string
	httpClient = http.DefaultClient
)

// Size in bytes
const (
	_  = iota
	KB = 1 << (10 * iota)
	MB
)

func main() {
	viper.SetConfigName("config")
	viper.AddConfigPath(".")
	log.SetFlags(0)
	err := viper.ReadInConfig()

	if err != nil {
		log.Fatal(err)
	} else {
		bucket = viper.GetString("s3.bucket")
	}

	router := httprouter.New()
	router.GET("/health", health)
	router.GET("/file/:filename", getFile)
	router.POST("/upload", handleUpload)
	router.POST("/uploadBase64", handleUploadBase64)
	router.GET("/resize/:size/*source", handleResize)

	// serve files
	router.ServeFiles("/static/*filepath", http.Dir("/tmp/"))

	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(viper.GetInt("server.port")), router))
}

func health(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	w.WriteHeader(http.StatusOK)
}

/* -----------------------   download  ----------------------- */
func getFile(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	fmt.Println("getFile")
	fmt.Println(params)

	// local
	if viper.GetBool("server.local") == true {
		localUrl := viper.GetString("server.static") + params.ByName("filename")
		fmt.Println("The local URL is", localUrl)
		w.Write([]byte(localUrl))
		return
	}

	// s3
	config := &aws.Config{
		Region: aws.String(viper.GetString("s3.region")),
		Credentials: credentials.NewStaticCredentials(
			viper.GetString("s3.access-key-id"),
			viper.GetString("s3.secret-access-key"),
			"",
		),
	}
	sess, err := session.NewSession(config)

	if err != nil {
		http.Error(w, err.Error(), 606)
		return
	}

	svc := s3.New(sess)
	bucket := viper.GetString("s3.bucket")
	var key = new(string)
	*key = "files/" + strings.Replace(params.ByName("filename"), " ", "_", -1)
	req, _ := svc.GetObjectRequest(&s3.GetObjectInput{
		Bucket: &bucket,
		Key:    key,
	})
	urlStr, err := req.Presign(time.Duration(viper.GetInt("s3.expireTimeMinutes")) * time.Minute)

	if err != nil {
		log.Println("Failed to sign request", err)
	}

	log.Println("The URL is", urlStr)
	w.Write([]byte(urlStr))
}


/* -----------------------   upload  ----------------------- */
func handleUpload(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	fmt.Println("handleUpload method:", r.Method)
	r.ParseMultipartForm(32 << 20)
	file, header, err := r.FormFile("uploadfile")
	if err != nil {
		w.Write([]byte(err.Error()))
		return
	}

	defer file.Close()

	contentType := header.Header.Get("Content-Type")
	fname := strings.Replace(header.Filename, " ", "_", -1)

	var fURL string
	if viper.GetBool("server.local") == true {
		// local
		bytes, err := ioutil.ReadAll(file)
		if err != nil {
			fmt.Println("error reading file", err)
			w.Write([]byte(err.Error()))
			return
		}

		fURL, err = saveToLocalFS(bytes, fname, contentType)

	} else {
		// upload
		fURL, err = uploadToS3(file, fname, contentType)
	}

	fileSize, err := file.Seek(0, 2)
	if err != nil {
		w.Write([]byte(err.Error()))
		return
	}
	fmt.Println("File size: ", fileSize)

	if err != nil {
		fmt.Println("error saving/uploading file", err)
		w.Write([]byte(err.Error()))
		return
	}

	fmt.Printf("fURL %s\n", fURL)
	w.Write([]byte(fURL))

}

func handleUploadBase64(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	fmt.Println("handleUploadBase64 method:", r.Method)
	body, err := ioutil.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		w.Write([]byte(err.Error()))
		return
	}

	contentType, data, err := getParts(string(body))
	if err != nil {
		w.Write([]byte(err.Error()))
		return
	}

	file, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		w.Write([]byte(err.Error()))
		return
	}

	fmt.Println("File size: ", len(file))

	fname := strings.Replace(r.Header.Get("File-Name"), " ", "_", -1)
	var fURL string
	if viper.GetBool("server.local") == true {
		// local
		fURL, err = saveToLocalFS(file, fname, contentType)
	} else {
		// upload
		buf := bytes.NewReader(file)
		fURL, err = uploadToS3(buf, fname, contentType)
	}

	if err != nil {
		fmt.Println("error saving/uploading", err)
		w.Write([]byte(err.Error()))
		return
	}

	fmt.Printf("fURL %s\n", fURL)
	w.Write([]byte(fURL))

}

func saveToLocalFS(file []byte, fname string, ftype string) (string, error) {
	log.Println("Begin save to local fs: ", fname)
	localFilePath := filepath.Join("/tmp/", fname)
	remoteFilePath := filepath.Join("/files/", fname)
	fmt.Printf("saveToLocalFS localFilePath: %s, remoteFilePath: %s \n", localFilePath, remoteFilePath)

	if err := ioutil.WriteFile(localFilePath, file, 0666); err != nil {
		fmt.Printf("error writing file: %s \n", err)
		return "", err
	}

	return remoteFilePath, nil
}

func uploadToS3(buf io.Reader, fname string, ftype string) (string, error) {
	log.Println("Begin uploadToS3: ", fname)

	config := &aws.Config{
		Region: aws.String(viper.GetString("s3.region")),
		Credentials: credentials.NewStaticCredentials(
			viper.GetString("s3.access-key-id"),
			viper.GetString("s3.secret-access-key"),
			"",
		),
	}

	sess, err := session.NewSession(config)
	uploader := s3manager.NewUploader(sess)

	// Perform an upload.
	h := md5.New()
	io.WriteString(h, fname)
	io.WriteString(h, time.Now().String())
	s := hex.EncodeToString(h.Sum(nil))
	fmt.Println("File type: %s", ftype)
	response, err := uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(viper.GetString("s3.bucket")),
		Key: aws.String("files/" + s + "-" + fname),
		Body: buf,
		ContentType: &ftype,
		ServerSideEncryption: aws.String("AES256"),
	})

	if err != nil {
		fmt.Printf("Error upload to S3: %s", err)
		return "", err
	}

	return response.Location, nil
}


/* -----------------------   resize  ----------------------- */
func handleResize(writer http.ResponseWriter, request *http.Request, params httprouter.Params) {
	sourcePath := request.URL.EscapedPath()
	width, height, err := parseWidthAndHeight(params.ByName("size"))

	if err != nil {
		http.Error(writer, err.Error(), 601)
		return
	}

	signature := request.Header.Get("Signature")

	if err = validateSignature(signature, sourcePath); err != nil {
		http.Error(writer, err.Error(), 602)
		return
	}

	source, err := url.Parse(strings.TrimPrefix(params.ByName("source"), "/"))

	if err != nil {
		http.Error(writer, err.Error(), 603)
		return
	}

	source.Scheme = ""
	source.Host = ""
	dir, file := path.Split(source.String())
	resultPath := strings.Join([]string{"cache/", dir, params.ByName("size"), "/", file}, "")

	if bucket == "" {
		body, e := getImageFromURL(source.String())

		if e != nil {
			http.Error(writer, e.Error(), 604)
			return
		}

		e = generateThumbnail(writer, body, sourcePath, width, height)

		if e != nil {
			http.Error(writer, e.Error(), 605)
			return
		}

		return
	}

	config := &aws.Config{
		Region: aws.String(viper.GetString("s3.region")),
		Credentials: credentials.NewStaticCredentials(
			viper.GetString("s3.access-key-id"),
			viper.GetString("s3.secret-access-key"),
			"",
		),
	}

	sess, err := session.NewSession(config)

	if err != nil {
		http.Error(writer, err.Error(), 606)
		return
	}

	input := &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(resultPath),
	}

	svc := s3.New(sess)
	output, err := svc.GetObject(input)

	if err != nil {
		source, err := url.Parse(strings.TrimPrefix(params.ByName("source"), "/"))

		if err != nil {
			http.Error(writer, err.Error(), 607)
			return
		}

		if source.Host == "" {
			input := &s3.GetObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(params.ByName("source")),
			}

			output, err = svc.GetObject(input)

			if err != nil {
				http.Error(writer, err.Error(), 608)
				return
			}

			err = generateThumbnail(writer, output.Body, resultPath, width, height)

			if err != nil {
				http.Error(writer, err.Error(), 609)
				return
			}
		} else {
			body, err := getImageFromURL(source.String())

			if err != nil {
				http.Error(writer, err.Error(), 610)
			}

			generateThumbnail(writer, body, resultPath, width, height)
			return
		}
	}

	setResultHeaders(writer, &result{
		ContentType:   *output.ContentType,
		ContentLength: *output.ContentLength,
		ETag:          *output.ETag,
		Path:          resultPath,
	})

	if _, err := io.Copy(writer, output.Body); err != nil {
		http.Error(writer, err.Error(), 611)
		return
	}
}

type result struct {
	Data          []byte
	ContentType   string
	ContentLength int64
	ETag          string
	Path          string
}

func computeHexMD5(data []byte) string {
	h := md5.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func generateThumbnail(writer http.ResponseWriter, body io.ReadCloser, path string, width, height int) error {
	img, err := ioutil.ReadAll(body)
	body.Close()

	if err != nil {
		return err
	}

	buf, err := vips.Resize(img, vips.Options{
		Height:       height,
		Width:        width,
		Crop:         viper.GetBool("vips.crop"),
		Interpolator: vips.BICUBIC,
		Gravity:      vips.CENTRE,
		Quality:      viper.GetInt("vips.quality"),
	})

	if err != nil {
		return err
	}

	var contentType string

	switch {
	case bytes.Equal(buf[:2], vips.MARKER_JPEG):
		contentType = "image/jpeg"
	case bytes.Equal(buf[:2], vips.MARKER_PNG):
		contentType = "image/png"
	default:
		return fmt.Errorf("Unknown image format")
	}

	result := &result{
		ContentType:   contentType,
		ContentLength: int64(len(buf)),
		Data:          buf,
		ETag:          computeHexMD5(buf),
		Path:          path,
	}

	setResultHeaders(writer, result)

	if _, err = writer.Write(buf); err != nil {
		return err
	}

	if bucket != "" {
		go storeResult(result)
	}

	return nil
}

func getImageFromURL(URL string) (io.ReadCloser, error) {
	response, err := httpClient.Get(URL)

	if err != nil {
		return nil, err
	}

	if response.StatusCode != 200 {
		return nil, fmt.Errorf("Unexpected status code from source: %d", response.StatusCode)
	}

	return response.Body, nil
}

func parseWidthAndHeight(str string) (width, height int, err error) {
	if value, ok := viper.GetStringMapString("sizes")[str]; ok {
		sizeParts := strings.Split(value, "x")

		if len(sizeParts) != 2 {
			return 0, 0, fmt.Errorf("Invalid size requested")
		}

		width, err = strconv.Atoi(sizeParts[0])

		if err != nil {
			return 0, 0, err
		}

		height, err = strconv.Atoi(sizeParts[1])

		if err != nil {
			return 0, 0, err
		}

		return width, height, nil
	}

	err = fmt.Errorf("Invalid size requested")
	return
}

func setCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", fmt.Sprintf("max-age=%d,public", viper.GetInt("cache-control.max-age")))
}

func setResultHeaders(w http.ResponseWriter, result *result) {
	w.Header().Set("Content-Type", result.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(result.ContentLength, 10))
	w.Header().Set("ETag", `"`+result.ETag+`"`)
	setCacheHeaders(w)
}

func storeResult(result *result) {
	config := &aws.Config{
		Region: aws.String(viper.GetString("s3.region")),
		Credentials: credentials.NewStaticCredentials(
			viper.GetString("s3.access-key-id"),
			viper.GetString("s3.secret-access-key"),
			"",
		),
	}

	session, err := session.NewSession(config)

	if err != nil {
		log.Fatal(err)
	}

	svc := s3.New(session)

	params := &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(result.Path),
		Body:          bytes.NewReader(result.Data),
		ContentLength: aws.Int64(result.ContentLength),
		ContentType:   aws.String(result.ContentType),
		StorageClass:  aws.String(s3.StorageClassReducedRedundancy),
	}

	_, err = svc.PutObject(params)

	if err != nil {
		log.Fatal(err)
	}
}

func validateSignature(sig, pathPart string) error {
	h := hmac.New(sha3.New256, []byte(viper.GetString("server.key")))

	if _, err := h.Write([]byte(pathPart)); err != nil {
		return err
	}

	actualSig := base64.StdEncoding.EncodeToString(h.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(sig), []byte(actualSig)) != 1 {
		return fmt.Errorf("Signature mismatch")
	}

	return nil
}

func getParts(s string) (string, string, error) {
	re := regexp.MustCompile("data:(.*);base64,(.*)")
	parts := re.FindStringSubmatch(s)

	if len(parts) < 3 {
		return "", "", errors.New("Invalid Base64 input")
	}

	return parts[1], parts[2], nil
}
