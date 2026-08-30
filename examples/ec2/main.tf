terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

# Offline provider config: this example is only ever planned (never applied),
# so fake credentials keep it runnable without an AWS account.
provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
}

variable "azs" {
  type    = list(string)
  default = ["us-east-1a", "us-east-1b"]
}

resource "aws_vpc" "app" {
  cidr_block           = "10.20.0.0/16"
  enable_dns_hostnames = true

  tags = {
    Name = "app-vpc"
  }
}

resource "aws_internet_gateway" "app" {
  vpc_id = aws_vpc.app.id
}

resource "aws_subnet" "web" {
  count                   = length(var.azs)
  vpc_id                  = aws_vpc.app.id
  availability_zone       = var.azs[count.index]
  cidr_block              = cidrsubnet("10.20.0.0/16", 8, count.index)
  map_public_ip_on_launch = true

  tags = {
    Name = "web-${var.azs[count.index]}"
  }
}

resource "aws_subnet" "app" {
  count             = length(var.azs)
  vpc_id            = aws_vpc.app.id
  availability_zone = var.azs[count.index]
  cidr_block        = cidrsubnet("10.20.0.0/16", 8, count.index + 10)

  tags = {
    Name = "app-${var.azs[count.index]}"
  }
}

resource "aws_route_table" "web" {
  vpc_id = aws_vpc.app.id
}

resource "aws_route" "web_internet" {
  route_table_id         = aws_route_table.web.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.app.id
}

resource "aws_route_table_association" "web" {
  count          = length(var.azs)
  subnet_id      = aws_subnet.web[count.index].id
  route_table_id = aws_route_table.web.id
}

resource "aws_security_group" "alb" {
  name_prefix = "alb-"
  vpc_id      = aws_vpc.app.id

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "app" {
  name_prefix = "app-"
  vpc_id      = aws_vpc.app.id

  ingress {
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_instance" "app" {
  count                  = length(var.azs)
  ami                    = "ami-0abcdef1234567890"
  instance_type          = "t3.small"
  subnet_id              = aws_subnet.app[count.index].id
  vpc_security_group_ids = [aws_security_group.app.id]

  tags = {
    Name = "app-server-${count.index}"
  }
}

resource "aws_instance" "bastion" {
  ami                    = "ami-0abcdef1234567890"
  instance_type          = "t3.micro"
  subnet_id              = aws_subnet.web[0].id
  vpc_security_group_ids = [aws_security_group.alb.id]

  tags = {
    Name = "bastion"
  }
}

resource "aws_ebs_volume" "data" {
  count             = length(var.azs)
  availability_zone = var.azs[count.index]
  size              = 20
  type              = "gp3"

  tags = {
    Name = "app-data-${count.index}"
  }
}

resource "aws_volume_attachment" "data" {
  count       = length(var.azs)
  device_name = "/dev/sdf"
  volume_id   = aws_ebs_volume.data[count.index].id
  instance_id = aws_instance.app[count.index].id
}

resource "aws_lb" "app" {
  name               = "app-alb"
  load_balancer_type = "application"
  subnets            = aws_subnet.web[*].id
  security_groups    = [aws_security_group.alb.id]
}

resource "aws_lb_target_group" "app" {
  name     = "app-tg"
  port     = 8080
  protocol = "HTTP"
  vpc_id   = aws_vpc.app.id
}

resource "aws_lb_target_group_attachment" "app" {
  count            = length(var.azs)
  target_group_arn = aws_lb_target_group.app.arn
  target_id        = aws_instance.app[count.index].id
  port             = 8080
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.app.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }
}
