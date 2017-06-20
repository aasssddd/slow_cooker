FROM library/golang:1.7.3

WORKDIR /go/src/slow_cooker

RUN go get github.com/tools/godep

ADD . /go/src/github.com/buoyantio/slow_cooker

WORKDIR /go/src/github.com/buoyantio/slow_cooker

RUN godep restore

RUN ls -l /go/pkg

RUN go build -o /go/bin/slow_cooker

ENTRYPOINT ["/go/bin/slow_cooker"]
