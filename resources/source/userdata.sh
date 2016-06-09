#!/bin/bash -xe
CONCOURSE_DOWNLOAD_URL=https://github.com/concourse/bin/releases/download/v1.3.0-rc.73/concourse_linux_amd64
PUBLIC_HOSTNAME=`ec2metadata --public-hostname`
PRIVATE_IP_ADDR=`ec2metadata --local-ipv4`

CONCOURSE_BASIC_AUTH_USERNAME={{ .Username }}
CONCOURSE_BASIC_AUTH_PASSWORD={{ .Password }}
SPARTA_CICD_BINARY_PATH=/home/ubuntu/{{ .ServiceName }}.lambda.amd64
POSTGRES_ADDRESS={ "Fn::GetAtt" : [ "{{ .DBInstanceResourceName }}" , "Endpoint.Address" ] }
POSTGRES_CONNECTION_STRING={{ .DBInstanceUser }}:{{ .DBInstancePassword }}@$POSTGRES_ADDRESS/{{ .DBInstanceDatabaseName }}

################################################################################
# 
# Tested on Ubuntu 16.04
#
# AMI: ubuntu/images/hvm-ssd/ubuntu-xenial-16.04-amd64-server-20160516.1 (ami-06b94666)
if [ ! -f "/home/ubuntu/userdata.sh" ]
then
  curl -vs http://169.254.169.254/latest/user-data -o /home/ubuntu/userdata.sh
  chmod +x /home/ubuntu/userdata.sh
  apt-get install supervisor -y
fi

# Install everything
service supervisor stop || apt-get install supervisor -y
apt-get update -y 
apt-get upgrade -y 
apt-get install supervisor awscli unzip git -y

################################################################################
# Our own binary
aws s3 cp s3://{{ .S3Bucket }}/{{ .S3Key }} /home/ubuntu/application.zip
unzip -o /home/ubuntu/application.zip -d /home/ubuntu
chmod +x $SPARTA_CICD_BINARY_PATH

################################################################################
# ConcourseCI
if [ ! -f "/home/ubuntu/concourse" ]
then
  curl -vs -L $CONCOURSE_DOWNLOAD_URL -o /home/ubuntu/concourse 
  chmod +x /home/ubuntu/concourse 
fi

rm -fv /home/ubuntu/*key*
ssh-keygen -t rsa -f /home/ubuntu/host_key -N '' 
ssh-keygen -t rsa -f /home/ubuntu/worker_key -N '' 
ssh-keygen -t rsa -f /home/ubuntu/session_signing_key -N '' 
cp /home/ubuntu/worker_key.pub /home/ubuntu/authorized_worker_keys

################################################################################
# SUPERVISOR
# REF: http://supervisord.org/
# Cleanout secondary directory
mkdir -pv /etc/supervisor/conf.d
  
CONCOURSE_WEB_SUPERVISOR_CONF="[program:concourse_web]
command=/home/ubuntu/concourse web --basic-auth-username $CONCOURSE_BASIC_AUTH_USERNAME --basic-auth-password $CONCOURSE_BASIC_AUTH_PASSWORD --session-signing-key /home/ubuntu/session_signing_key --tsa-host-key /home/ubuntu/host_key --tsa-authorized-keys /home/ubuntu/authorized_worker_keys --postgres-data-source postgres://$POSTGRES_CONNECTION_STRING --external-url http://$PUBLIC_HOSTNAME:8080 --peer-url http://$PRIVATE_IP_ADDR:8080
numprocs=1
directory=/tmp
priority=999
autostart=true
autorestart=unexpected
startsecs=10
startretries=3
exitcodes=0,2
stopsignal=TERM
stopwaitsecs=10
stopasgroup=false
killasgroup=false
user=ubuntu
stdout_logfile=/var/log/concourse_web.log
stdout_logfile_maxbytes=1MB
stdout_logfile_backups=10
stdout_capture_maxbytes=1MB
stdout_events_enabled=false
redirect_stderr=false
stderr_logfile=concourse_web.err.log
stderr_logfile_maxbytes=1MB
stderr_logfile_backups=10
stderr_capture_maxbytes=1MB
stderr_events_enabled=false
"
echo "$CONCOURSE_WEB_SUPERVISOR_CONF" > /etc/supervisor/conf.d/concourse_web.conf


CONCOURSE_WORKER_SUPERVISOR_CONF="[program:concourse_worker]
command=/home/ubuntu/concourse worker --work-dir /opt/concourse/worker --tsa-host 127.0.0.1 --tsa-public-key /home/ubuntu/host_key.pub  --tsa-worker-private-key /home/ubuntu/worker_key
numprocs=1
directory=/tmp
priority=999
autostart=true
autorestart=unexpected
startsecs=10
startretries=3
exitcodes=0,2
stopsignal=TERM
stopwaitsecs=10
stopasgroup=false
killasgroup=false
user=root
stdout_logfile=/var/log/concourse_worker.log
stdout_logfile_maxbytes=1MB
stdout_logfile_backups=10
stdout_capture_maxbytes=1MB
stdout_events_enabled=false
redirect_stderr=false
stderr_logfile=concourse_worker.err.log
stderr_logfile_maxbytes=1MB
stderr_logfile_backups=10
stderr_capture_maxbytes=1MB
stderr_events_enabled=false
"
echo "$CONCOURSE_WORKER_SUPERVISOR_CONF" > /etc/supervisor/conf.d/concourse_worker.conf

SPARTA_CI_CD_SYNC_SUPERVISOR_CONF="[program:spartasync]
command=$SPARTA_CICD_BINARY_PATH sync --username $CONCOURSE_BASIC_AUTH_USERNAME --password $CONCOURSE_BASIC_AUTH_PASSWORD
numprocs=1
directory=/tmp
priority=999
autostart=true
autorestart=unexpected
startsecs=10
startretries=3
exitcodes=0,2
stopsignal=TERM
stopwaitsecs=10
stopasgroup=false
killasgroup=false
user=ubuntu
stdout_logfile=/var/log/spartasync.log
stdout_logfile_maxbytes=1MB
stdout_logfile_backups=10
stdout_capture_maxbytes=1MB
stdout_events_enabled=false
redirect_stderr=false
stderr_logfile=spartasync.err.log
stderr_logfile_maxbytes=1MB
stderr_logfile_backups=10
stderr_capture_maxbytes=1MB
stderr_events_enabled=false
"
echo "$SPARTA_CI_CD_SYNC_SUPERVISOR_CONF" > /etc/supervisor/conf.d/spartasync.conf

# Patch up the directory
chown -R ubuntu:ubuntu /home/ubuntu

# Startup Supervisor
service supervisor restart || service supervisor start