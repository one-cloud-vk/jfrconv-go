"""Small independent protobuf wire reader used only by validation tools."""


def varint(data, pos):
    value = 0
    for shift in range(0, 70, 7):
        if pos >= len(data):
            raise ValueError('truncated protobuf varint')
        byte = data[pos]
        pos += 1
        if shift == 63 and byte > 1:
            raise ValueError('protobuf varint exceeds 64 bits')
        value |= (byte & 127) << shift
        if byte < 128:
            return value, pos
    raise ValueError('protobuf varint too long')


def protobuf(data):
    pos = 0
    fields = []
    while pos < len(data):
        key, pos = varint(data, pos)
        number, wire = key >> 3, key & 7
        if not number:
            raise ValueError('zero protobuf field number')
        if wire == 0:
            value, pos = varint(data, pos)
        elif wire == 2:
            size, pos = varint(data, pos)
            value = data[pos:pos + size]
            if len(value) != size:
                raise ValueError('truncated protobuf field')
            pos += size
        elif wire in (1, 5):
            size = 8 if wire == 1 else 4
            value = data[pos:pos + size]
            if len(value) != size:
                raise ValueError('truncated fixed protobuf field')
            pos += size
        else:
            raise ValueError(f'unsupported protobuf wire type {wire}')
        fields.append((number, wire, value))
    return fields


def integers(fields, number):
    result = []
    for n, wire, value in fields:
        if n != number:
            continue
        if wire == 0:
            result.append(value)
        elif wire == 2:
            pos = 0
            while pos < len(value):
                integer, pos = varint(value, pos)
                result.append(integer)
        else:
            raise AssertionError('expected varint or packed varints')
    return result
