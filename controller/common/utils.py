def string_to_array(str_val, separator):
    """
    Args
        str_val : string value
        separator : string separator
    Return
        List as splitted string by separator after stripping whitespaces from each element
    """
    if not str_val:
        return []
    str_val = str_val.strip()
    res = str_val.split(separator)
    print('res {0} {1}'.format(res, type(res)))
    return [res_val.strip() for res_val in res]

