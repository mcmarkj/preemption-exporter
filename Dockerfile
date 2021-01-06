FROM alpine:latest

RUN apk add --no-cache git make musl-dev go

# Configure Go
ENV GOROOT /usr/lib/go
ENV GOPATH /go
ENV PATH /go/bin:$PATH

RUN mkdir -p ${GOPATH}/src ${GOPATH}/bin

# Install Glide
RUN go get -u github.com/Masterminds/glide/...



RUN go get github.com/prometheus/promu
RUN go get github.com/mcmarkj/preemption-exporter
WORKDIR /root/

COPY . /root/

RUN promu build


ENTRYPOINT ["/root/preemption-exporter"]
