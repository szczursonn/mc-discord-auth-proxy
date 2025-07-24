package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
)

func readByte(reader io.Reader) (byte, error) {
	buff, err := readBytes(reader, 1)
	if err != nil {
		return 0, err
	}
	return buff[0], nil
}

func readBytes(reader io.Reader, n int) (buff []byte, err error) {
	buff = make([]byte, n)
	_, err = io.ReadFull(reader, buff)
	return
}

func readVarInt(reader io.Reader) (int, error) {
	value := 0
	position := 0

	for {
		currentByte, err := readByte(reader)
		if err != nil {
			return 0, err
		}

		value |= (int(currentByte) & 0x7F) << (position)

		if (currentByte & 0x80) == 0 {
			break
		}

		position += 7
		if position >= 32 {
			return 0, fmt.Errorf("varint is too big (>5 bytes)")
		}
	}

	return value, nil
}

func readString(reader io.Reader) (string, error) {
	stringLength, err := readVarInt(reader)
	if err != nil {
		return "", err
	}

	buff, err := readBytes(reader, int(stringLength))
	if err != nil {
		return "", err
	}

	return string(buff), nil
}

func readUnsignedShort(reader io.Reader) (uint16, error) {
	var value uint16
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}
