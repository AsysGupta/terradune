terraform {
  required_providers {
    random = {
      source = "hashicorp/random"
    }
    local = {
      source = "hashicorp/local"
    }
  }
}

module "pets" {
  source   = "./pets"
  pet_count = 2
}

resource "local_file" "roster" {
  filename = "${path.module}/roster.txt"
  content  = join("\n", module.pets.names)
}
