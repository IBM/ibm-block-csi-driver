def get_array_connection_info_from_secret(secrets):
    user = secrets["username"]
    password = secrets["password"]
    array_addresses = secrets["management_address"].split(",")
    array_type = secrets["array_type"] or None
    return user, password, array_addresses, array_type
