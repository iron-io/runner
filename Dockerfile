FROM treeder/ruby-dind

WORKDIR /app
ADD runner /app

ENTRYPOINT ["./runner"]
