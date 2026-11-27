package tv

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"testing"
)

func TestIPControlReadsFragmentedEncryptedResponse(t *testing.T) {
	// These fixtures use a test keycode, a fixed IV, and an OpenSSL-generated reply.
	key, _ := hex.DecodeString("c5feac1b00c48ac7740ad3945e354f0f")
	reply, _ := hex.DecodeString("812c52a0bcadf3be43c42ae052c9dc2bd1e28501a4877f5bd70379e190928337")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	client, peer := net.Pipe()
	defer client.Close()
	finished := make(chan error, 1)
	go func() {
		defer peer.Close()
		wire := make([]byte, 3*aes.BlockSize)
		if _, err := io.ReadFull(peer, wire); err != nil {
			finished <- err
			return
		}
		iv, message := wire[:aes.BlockSize], wire[aes.BlockSize:]
		block.Decrypt(iv, iv)
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(message, message)
		command := []byte("KEY_ACTION number0\r")
		padding := len(message) - len(command)
		want := append(command, bytes.Repeat([]byte{byte(padding)}, padding)...)
		if !bytes.Equal(message, want) {
			finished <- fmt.Errorf("unexpected command frame: %q", message)
			return
		}
		for _, fragment := range [][]byte{reply[:7], reply[7:18], reply[18:]} {
			if _, err := peer.Write(fragment); err != nil {
				finished <- err
				return
			}
		}
		finished <- nil
	}()
	if err := sendIPKey(client, block, "number0"); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}
