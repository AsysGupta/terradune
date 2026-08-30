variable "pet_count" {
  type = number
}

resource "random_pet" "these" {
  count  = var.pet_count
  length = 2
}

resource "local_file" "note" {
  filename = "${path.module}/note.txt"
  content  = join(",", random_pet.these[*].id)
}

output "names" {
  value = random_pet.these[*].id
}
