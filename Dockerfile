FROM joelchen/golang-vips:latest
RUN go get github.com/iHealthLab/gothumb
COPY . /go/src/github.com/iHealthLab/gothumb
RUN cd /go/src/github.com/iHealthLab/gothumb && go build -o /go/bin/gothumb
