package slice

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"github.com/bradfitz/iter"
	"github.com/cheggaaa/pb"
	"github.com/dustin/go-humanize"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

type sliceTask struct {
	elog *log.Logger
}

// Slice creates n subparts from the gzip'd JSON file at `filename`.
func Slice(el *log.Logger, filename string, n int) (filenames []string, err error) {
	slicer := sliceTask{elog: el}

	// capture errors thrown by `must` helpers
	defer func() {
		r := recover()
		if rerr, ok := r.(error); ok {
			err = rerr
		} else if r != nil {
			panic(r)
		}
	}()

	inputfile, size := mustOpen(el, filename)
	defer func() { err = inputfile.Close() }()

	log.Printf("creating %d output files", n)
	basename := filepath.Base(filename)
	outputs := make([]io.Writer, n)
	for i := range iter.N(n) {
		outfilename := fmt.Sprintf("%d_%s", i, basename)
		filenames = append(filenames, outfilename)

		outf := mustCreate(el, outfilename)
		outbuf := bufio.NewWriter(outf)
		gzw := gzip.NewWriter(outbuf)
		outputs[i] = gzw
		log.Printf("\toutput file %d: %q", i, outfilename)
		defer func(filename string) {
			if err := gzw.Close(); err != nil {
				el.Printf("closing gzip stream for file %q", outfilename)
			}

			if err := outbuf.Flush(); err != nil {
				el.Printf("flushing buffered stream for file %q", outfilename)
			}
			if err := outf.Close(); err != nil {
				el.Printf("closing file %q", outfilename)
			}
		}(outfilename)
	}

	lines := make(chan []byte, n*2)
	doneWrite := make(chan struct{})
	start := time.Now()
	go slicer.multiplexLines(lines, outputs, doneWrite)

	log.Printf("reading lines from %q (%s)", filename, humanize.Bytes(uint64(size)))
	if err := readLines(inputfile, size, lines); err != nil {
		el.Printf("reading lines from input failed: %v", err)
	}
	log.Printf("done reading lines in %v", time.Since(start))

	<-doneWrite
	log.Printf("done writing to outputs in %v", time.Since(start))

	return filenames, nil
}

func (st *sliceTask) multiplexLines(lines <-chan []byte, outputs []io.Writer, done chan<- struct{}) {
	defer close(done)
	outIdx := 0
	outMod := len(outputs)
	for line := range lines {
		_, err := outputs[outIdx].Write(line)
		if err != nil {
			st.elog.Printf("couldn't write to output %d: %v", outIdx, err)
			return
		}
		outIdx = (outIdx + 1) % outMod
	}
}

func readLines(r io.Reader, size int64, lines chan<- []byte) error {
	defer close(lines)
	bar := pb.New(int(size))
	bar.ShowBar = true
	bar.ShowCounters = true
	bar.ShowPercent = true
	bar.ShowSpeed = true
	bar.ShowTimeLeft = true
	bar.SetUnits(pb.U_BYTES)
	barr := bar.NewProxyReader(r)

	gr, err := gzip.NewReader(barr)
	if err != nil {
		return err
	}
	defer func() { _ = gr.Close() }()

	rd := bufio.NewReader(gr)

	bar.Start()
	defer bar.FinishPrint("all lines were read")
	var total uint64
	defer func() { log.Printf("total decompressed size %s", humanize.Bytes(total)) }()
	for {
		line, err := rd.ReadBytes('\n')
		switch err {
		case io.EOF:
			return nil
		case nil:
		default:
			return err
		}
		total += uint64(len(line) + 1)
		lines <- line
	}
}

func mustOpen(elog *log.Logger, filename string) (*os.File, int64) {
	file, err := os.Open(filename)
	if err != nil {
		elog.Panicf("couldn't open file %q: %v", filename, err)
	}
	fi, err := file.Stat()
	if err != nil {
		_ = file.Close()
		elog.Panicf("couldn't stat file %q: %v", filename, err)
	}
	return file, fi.Size()
}

func mustCreate(elog *log.Logger, filename string) *os.File {
	file, err := os.Create(filename)
	if err != nil {
		elog.Panicf("couldn't create file %q: %v", filename, err)
	}
	return file
}