package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

// Client is an interface for generically interacting with Ethereum clients.
type Client interface {
	// Start starts client, but does not wait for the command to exit.
	Start(ctx context.Context, verbose bool) error

	// HttpAddr returns the address where the client is servering its
	// JSON-RPC.
	HttpAddr() string

	// Close closes the client.
	Close() error
}

// gethClient is a wrapper around a go-ethereum instance on a separate thread.
type gethClient struct {
	cmd     *exec.Cmd
	path    string
	workdir string
	blocks  []*types.Block
	genesis *core.Genesis
}

// newGethClient instantiates a new GethClient.
//
// The client's data directory is set to a temporary location and it
// initializes with the genesis and the provided blocks.
func newGethClient(ctx context.Context, path string, genesis *core.Genesis, blocks []*types.Block, verbose bool) (*gethClient, error) {
	tmp, err := os.MkdirTemp("", "rpctestgen-*")
	if err != nil {
		return nil, err
	}
	if err := writeGenesis(fmt.Sprintf("%s/genesis.json", tmp), genesis); err != nil {
		return nil, err
	}
	if err := writeChain(fmt.Sprintf("%s/chain.rlp", tmp), blocks); err != nil {
		return nil, err
	}

	var (
		args     = ctx.Value(ARGS).(*Args)
		datadir  = fmt.Sprintf("--datadir=%s", tmp)
		gcmode   = "--gcmode=archive"
		loglevel = fmt.Sprintf("--verbosity=%d", args.logLevelInt)
	)

	// Run geth init.
	options := []string{datadir, gcmode, loglevel, "init", fmt.Sprintf("%s/genesis.json", tmp)}
	err = runCmd(ctx, path, verbose, options...)
	if err != nil {
		return nil, err
	}

	// Run geth import.
	options = []string{datadir, gcmode, loglevel, "import", fmt.Sprintf("%s/chain.rlp", tmp)}
	err = runCmd(ctx, path, verbose, options...)
	if err != nil {
		return nil, err
	}

	return &gethClient{path: path, genesis: genesis, blocks: blocks, workdir: tmp}, nil
}

// Start starts geth, but does not wait for the command to exit.
func (g *gethClient) Start(ctx context.Context, verbose bool) error {
	fmt.Println("starting client")
	// TODO: check geth version
	var (
		args    = ctx.Value(ARGS).(*Args)
		options = []string{
			fmt.Sprintf("--datadir=%s", g.workdir),
			fmt.Sprintf("--verbosity=%d", args.logLevelInt),
			fmt.Sprintf("--port=%s", NETWORKPORT),
			"--gcmode=archive",
			"--nodiscover",
			"--dev",
			"--dev.period=0", // 0 = mine only if transaction pending
			"--http",
			"--http.api=admin,eth,debug",
			fmt.Sprintf("--http.addr=%s", HOST),
			fmt.Sprintf("--http.port=%s", PORT),
		}
	)
	g.cmd = exec.CommandContext(
		ctx,
		g.path,
		options...,
	)
	if verbose {
		g.cmd.Stdout = os.Stdout
		g.cmd.Stderr = os.Stderr
	}
	if err := g.cmd.Start(); err != nil {
		return err
	}
	return nil
}

// HttpAddr returns the address where the client is servering its JSON-RPC.
func (g *gethClient) HttpAddr() string {
	return fmt.Sprintf("http://%s:%s", HOST, PORT)
}

// Close closes the client.
func (g *gethClient) Close() error {
	g.cmd.Process.Kill()
	g.cmd.Wait()
	return os.RemoveAll(g.workdir)
}

// runCmd runs a command and outputs the command's stdout and stderr to the
// caller's stdout and stderr if verbose is set.
func runCmd(ctx context.Context, path string, verbose bool, args ...string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	if verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

// writeGenesis writes the genesis to disk.
func writeGenesis(filename string, genesis *core.Genesis) error {
	out, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filename, out, 0644); err != nil {
		return err
	}
	return nil
}

// writeChain writes a chain to disk.
func writeChain(filename string, blocks []*types.Block) error {
	w, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer w.Close()
	for _, block := range blocks {
		if err := rlp.Encode(w, block); err != nil {
			return err
		}
	}
	return nil
}

// readChain reads a chain.rlp file to a slice of Block.
func readChain(filename string) ([]*types.Block, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var (
		stream = rlp.NewStream(f, 0)
		blocks = make([]*types.Block, 0)
		i      = 0
	)
	for {
		var b types.Block
		if err := stream.Decode(&b); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("at block %d: %v", i, err)
		}
		blocks = append(blocks, &b)
		i++
	}
	return blocks, nil
}
