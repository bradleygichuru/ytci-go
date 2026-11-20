FROM alpine:3.20
RUN apk add --no-cache postgresql16-client aws-cli bash coreutils
COPY backup-cron.sh /usr/local/bin/backup-cron.sh
RUN chmod +x /usr/local/bin/backup-cron.sh
ENTRYPOINT ["/usr/local/bin/backup-cron.sh"]
