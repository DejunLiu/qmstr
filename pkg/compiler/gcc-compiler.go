package compiler

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	pb "github.com/QMSTR/qmstr/pkg/buildservice"
	"github.com/QMSTR/qmstr/pkg/wrapper"
	"github.com/spf13/pflag"
)

type mode int

const (
	Link mode = iota
	Preproc
	Compile
	Assemble
	PrintOnly
)
const undef = "undef"

var (
	boolArgs = map[string]struct{}{
		"-w":               struct{}{},
		"-W":               struct{}{},
		"-O":               struct{}{},
		"-f":               struct{}{},
		"-C":               struct{}{},
		"-std":             struct{}{},
		"-nostdinc":        struct{}{},
		"-print-file-name": struct{}{},
		"-MD":              struct{}{},
		"-m":               struct{}{},
		"-v":               struct{}{},
		"-g":               struct{}{},
		"-pg":              struct{}{},
		"-P":               struct{}{},
		"-pipe":            struct{}{},
		"--version":        struct{}{},
	}

	stringArgs = map[string]struct{}{
		"-D": struct{}{},
		"-U": struct{}{},
		"-x": struct{}{},
	}

	stringArgsRE = "\\s+\\w+={0,1}\\S*\\s"

	fixPosixArgs = map[string]struct{}{
		"-isystem": struct{}{},
		"-include": struct{}{},
	}
)

type GccCompiler struct {
	Mode    mode
	Input   []string
	Output  []string
	WorkDir string
	Args    []string
	logger  *log.Logger
}

func NewGccCompiler(workDir string, logger *log.Logger) *GccCompiler {
	return &GccCompiler{Link, []string{}, []string{}, workDir, []string{}, logger}
}

func (g *GccCompiler) Analyze(commandline []string) (*pb.BuildMessage, error) {
	g.logger.Printf("Parsing commandline %v", commandline)
	g.parseCommandLine(commandline[1:])

	switch g.Mode {
	case Link:
		g.logger.Printf("gcc linking")
		buildLinkMsg := pb.BuildMessage_Link{Target: &pb.File{Path: g.Output[0]}}
		dependencies := []*pb.File{}
		for _, inFile := range g.Input {
			inputFile := pb.File{Path: wrapper.BuildCleanPath(g.WorkDir, inFile)}
			dependencies = append(dependencies, &inputFile)
		}
		buildLinkMsg.Dependencies = dependencies
		buildMsg := pb.BuildMessage{}
		buildMsg.Binary = []*pb.BuildMessage_Link{&buildLinkMsg}

		return &buildMsg, nil
	case Assemble:
		g.logger.Printf("gcc assembling - skipping link")
		buildMsg := pb.BuildMessage{}
		buildMsg.Compilations = []*pb.BuildMessage_Compile{}

		for idx, inFile := range g.Input {
			g.logger.Printf("This is the source file %s indexed %d", inFile, idx)
			sourceFile := pb.File{Path: wrapper.BuildCleanPath(g.WorkDir, inFile)}
			targetFile := pb.File{Path: wrapper.BuildCleanPath(g.WorkDir, g.Output[idx])}
			buildMsg.Compilations = append(buildMsg.Compilations, &pb.BuildMessage_Compile{Source: &sourceFile, Target: &targetFile})
		}
		return &buildMsg, nil
	default:
		return nil, errors.New("Mode not implemented")
	}
}

func (g *GccCompiler) cleanCmdLine(args []string) {
	clearIdxSet := map[int]struct{}{}
	for idx, arg := range args {
		g.logger.Printf("%d - %s", idx, arg)

		// index string flags
		if idx < len(args)-1 {
			for key := range stringArgs {
				g.logger.Printf("Find %s string arg in %s with %s", key, fmt.Sprintf("%s %s ", arg, args[idx+1]), fmt.Sprintf("%s%s", key, stringArgsRE))
				re := regexp.MustCompile(fmt.Sprintf("%s%s", key, stringArgsRE))
				if re.MatchString(fmt.Sprintf("%s %s ", arg, args[idx+1])) {
					g.logger.Printf("Found %v string arg", args[idx:idx+1])
					clearIdxSet[idx] = struct{}{}
					clearIdxSet[idx+1] = struct{}{}
				}
				if strings.HasPrefix(arg, key) {
					clearIdxSet[idx] = struct{}{}
				}
			}
		}

		// index bool flags
		for key := range boolArgs {
			if strings.HasPrefix(arg, key) {
				clearIdxSet[idx] = struct{}{}
			}
		}

		// fix long arguments to pass through pflags
		for key := range fixPosixArgs {
			if key == arg {
				args[idx] = fmt.Sprintf("-%s", arg)
			}
		}
	}

	clear := []int{}
	for k := range clearIdxSet {
		clear = append(clear, k)
	}
	sort.Sort(sort.IntSlice(clear))

	g.logger.Printf("To be cleaned %v", clear)
	initialArgsSize := len(args)
	for _, idx := range clear {
		g.logger.Printf("Clearing %d", idx)
		offset := initialArgsSize - len(args)
		offsetIdx := idx - offset
		g.logger.Printf("Actually clearing %d", offsetIdx)
		if initialArgsSize-1 == idx {
			g.logger.Printf("Cut last arg")
			args = args[:offsetIdx]
		} else {
			args = append(args[:offsetIdx], args[offsetIdx+1:]...)
		}
		g.logger.Printf("new slice is %v", args)
	}
	g.Args = args
}

func (g *GccCompiler) parseCommandLine(args []string) {
	g.logger.Printf("Parsing arguments: %v", args)

	// remove all flags we don't care about but that would break parsing
	g.cleanCmdLine(args)

	gccFlags := pflag.NewFlagSet("gcc", pflag.ContinueOnError)
	gccFlags.BoolP("assemble", "c", false, "do not link")
	gccFlags.BoolP("compile", "S", false, "do not assemble")
	gccFlags.BoolP("preprocess", "E", false, "do not compile")
	gccFlags.StringP("output", "o", undef, "output")
	gccFlags.StringSliceP("includepath", "I", []string{}, "include path")
	gccFlags.String("isystem", undef, "system include path")
	gccFlags.String("include", undef, "include header file")
	gccFlags.StringSliceP("linklib", "l", []string{}, "include header file")

	g.logger.Printf("Parsing cleaned commandline: %v", g.Args)
	err := gccFlags.Parse(g.Args)
	if err != nil {
		g.logger.Fatalf("Unrecoverable commandline parsing error: %s", err)
	}

	g.Input = gccFlags.Args()

	if ok, err := gccFlags.GetBool("assemble"); ok && err == nil {
		g.Mode = Assemble
	}
	if ok, err := gccFlags.GetBool("compile"); ok && err == nil {
		g.Mode = Compile
	}
	if ok, err := gccFlags.GetBool("preprocess"); ok && err == nil {
		g.Mode = Preproc
	}

	if output, err := gccFlags.GetString("output"); err == nil && output != undef {
		g.Output = []string{output}
	} else {
		// no output defined
		switch g.Mode {
		case Link:
			if len(g.Input) == 0 {
				// No input no output
				g.Mode = PrintOnly
				return
			}
			g.Output = []string{"a.out"}
		case Assemble:
			for _, input := range g.Input {
				objectname := strings.TrimSuffix(input, filepath.Ext(input)) + ".o"
				g.Output = append(g.Output, objectname)
			}
		}
	}
}
