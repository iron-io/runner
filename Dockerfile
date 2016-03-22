#FROM treeder/ruby-dind
FROM iron/go

WORKDIR /app
ADD runner /app

ENTRYPOINT ["./runner"]
