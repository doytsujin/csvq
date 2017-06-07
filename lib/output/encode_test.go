package output

import (
	"testing"

	"github.com/mithrandie/csvq/lib/cmd"
	"github.com/mithrandie/csvq/lib/parser"
	"github.com/mithrandie/csvq/lib/query"
	"github.com/mithrandie/csvq/lib/ternary"
)

var encodeViewTests = []struct {
	Name           string
	View           *query.View
	Format         cmd.Format
	LineBreak      cmd.LineBreak
	WriteDelimiter rune
	WithoutHeader  bool
	Result         string
	Error          string
}{
	{
		Name: "Empty Records",
		View: &query.View{
			Header:  query.NewHeaderWithoutId("test", []string{"c1", "c2\nsecond line", "c3"}),
			Records: []query.Record{},
		},
		Format: cmd.TEXT,
		Result: "Empty Records\n",
	},
	{
		Name: "Empty Fields",
		View: &query.View{
			Header: query.NewHeaderWithoutId("", []string{}),
			Records: []query.Record{
				query.NewRecordWithoutId([]parser.Primary{parser.NewNull()}),
			},
		},
		Format: cmd.TEXT,
		Result: "Empty Fields\n",
	},
	{
		Name: "Text",
		View: &query.View{
			Header: query.NewHeaderWithoutId("test", []string{"c1", "c2\nsecond line", "c3"}),
			Records: []query.Record{
				query.NewRecordWithoutId([]parser.Primary{parser.NewInteger(-1), parser.NewTernary(ternary.UNKNOWN), parser.NewBoolean(true)}),
				query.NewRecordWithoutId([]parser.Primary{parser.NewFloat(2.0123), parser.NewDatetimeFromString("2016-02-01 16:00:00.123456"), parser.NewString("abcdef")}),
				query.NewRecordWithoutId([]parser.Primary{parser.NewInteger(34567890), parser.NewString(" abcdefghijklmnopqrstuvwxyzabcdefg\nhi\"jk\n"), parser.NewNull()}),
			},
		},
		Format: cmd.TEXT,
		Result: "+----------+-----------------------------------+--------+\n" +
			"| c1       | c2                                | c3     |\n" +
			"|          | second line                       |        |\n" +
			"+----------+-----------------------------------+--------+\n" +
			"|       -1 |                           UNKNOWN |   true |\n" +
			"|   2.0123 | 2016-02-01 16:00:00.123456        | abcdef |\n" +
			"| 34567890 | abcdefghijklmnopqrstuvwxyzabcdefg |   NULL |\n" +
			"|          | hi\"jk                             |        |\n" +
			"+----------+-----------------------------------+--------+\n",
	},
	{
		Name: "CSV",
		View: &query.View{
			Header: query.NewHeaderWithoutId("test", []string{"c1", "c2\nsecond line", "c3"}),
			Records: []query.Record{
				query.NewRecordWithoutId([]parser.Primary{parser.NewInteger(-1), parser.NewTernary(ternary.UNKNOWN), parser.NewBoolean(true)}),
				query.NewRecordWithoutId([]parser.Primary{parser.NewFloat(2.0123), parser.NewDatetimeFromString("2016-02-01 16:00:00.123456"), parser.NewString("abcdef")}),
				query.NewRecordWithoutId([]parser.Primary{parser.NewInteger(34567890), parser.NewString(" abcdefghijklmnopqrstuvwxyzabcdefg\nhi\"jk\n"), parser.NewNull()}),
			},
		},
		Format: cmd.CSV,
		Result: "\"c1\",\"c2\nsecond line\",\"c3\"\n" +
			"-1,false,true\n" +
			"2.0123,\"2016-02-01 16:00:00.123456\",\"abcdef\"\n" +
			"34567890,\" abcdefghijklmnopqrstuvwxyzabcdefg\nhi\"\"jk\n\",",
	},
	{
		Name: "TSV",
		View: &query.View{
			Header: query.NewHeaderWithoutId("test", []string{"c1", "c2\nsecond line", "c3"}),
			Records: []query.Record{
				query.NewRecordWithoutId([]parser.Primary{parser.NewInteger(-1), parser.NewTernary(ternary.UNKNOWN), parser.NewBoolean(true)}),
				query.NewRecordWithoutId([]parser.Primary{parser.NewFloat(2.0123), parser.NewDatetimeFromString("2016-02-01 16:00:00.123456"), parser.NewString("abcdef")}),
				query.NewRecordWithoutId([]parser.Primary{parser.NewInteger(34567890), parser.NewString(" abcdefghijklmnopqrstuvwxyzabcdefg\nhi\"jk\n"), parser.NewNull()}),
			},
		},
		Format:         cmd.TSV,
		WriteDelimiter: '\t',
		Result: "\"c1\"\t\"c2\nsecond line\"\t\"c3\"\n" +
			"-1\tfalse\ttrue\n" +
			"2.0123\t\"2016-02-01 16:00:00.123456\"\t\"abcdef\"\n" +
			"34567890\t\" abcdefghijklmnopqrstuvwxyzabcdefg\nhi\"\"jk\n\"\t",
	},
	{
		Name: "CSV WithoutHeader",
		View: &query.View{
			Header: query.NewHeaderWithoutId("test", []string{"c1", "c2\nsecond line", "c3"}),
			Records: []query.Record{
				query.NewRecordWithoutId([]parser.Primary{parser.NewInteger(-1), parser.NewTernary(ternary.UNKNOWN), parser.NewBoolean(true)}),
				query.NewRecordWithoutId([]parser.Primary{parser.NewFloat(2.0123), parser.NewDatetimeFromString("2016-02-01 16:00:00.123456"), parser.NewString("abcdef")}),
				query.NewRecordWithoutId([]parser.Primary{parser.NewInteger(34567890), parser.NewString(" abcdefghijklmnopqrstuvwxyzabcdefg\nhi\"jk\n"), parser.NewNull()}),
			},
		},
		Format:        cmd.CSV,
		WithoutHeader: true,
		Result: "-1,false,true\n" +
			"2.0123,\"2016-02-01 16:00:00.123456\",\"abcdef\"\n" +
			"34567890,\" abcdefghijklmnopqrstuvwxyzabcdefg\nhi\"\"jk\n\",",
	},
	{
		Name: "CSV Line Break CRLF",
		View: &query.View{
			Header: query.NewHeaderWithoutId("test", []string{"c1", "c2\nsecond line", "c3"}),
			Records: []query.Record{
				query.NewRecordWithoutId([]parser.Primary{parser.NewInteger(-1), parser.NewTernary(ternary.UNKNOWN), parser.NewBoolean(true)}),
				query.NewRecordWithoutId([]parser.Primary{parser.NewFloat(2.0123), parser.NewDatetimeFromString("2016-02-01 16:00:00.123456"), parser.NewString("abcdef")}),
				query.NewRecordWithoutId([]parser.Primary{parser.NewInteger(34567890), parser.NewString(" abcdefghijklmnopqrstuvwxyzabcdefg\nhi\"jk\n"), parser.NewNull()}),
			},
		},
		Format:    cmd.CSV,
		LineBreak: cmd.CRLF,
		Result: "\"c1\",\"c2\r\nsecond line\",\"c3\"\r\n" +
			"-1,false,true\r\n" +
			"2.0123,\"2016-02-01 16:00:00.123456\",\"abcdef\"\r\n" +
			"34567890,\" abcdefghijklmnopqrstuvwxyzabcdefg\r\nhi\"\"jk\r\n\",",
	},
	{
		Name: "JSON",
		View: &query.View{
			Header: query.NewHeaderWithoutId("test", []string{"c1", "c2\nsecond line", "c3"}),
			Records: []query.Record{
				query.NewRecordWithoutId([]parser.Primary{parser.NewInteger(-1), parser.NewTernary(ternary.UNKNOWN), parser.NewBoolean(true)}),
				query.NewRecordWithoutId([]parser.Primary{parser.NewFloat(2.0123), parser.NewDatetimeFromString("2016-02-01 16:00:00.123456"), parser.NewString("abcdef")}),
				query.NewRecordWithoutId([]parser.Primary{parser.NewInteger(34567890), parser.NewString(" abc\\defghi/jklmn\topqrstuvwxyzabcdefg\nhi\"jk\n"), parser.NewNull()}),
			},
		},
		Format: cmd.JSON,
		Result: "[" +
			"{" +
			"\"c1\":-1," +
			"\"c2\\nsecond line\":false," +
			"\"c3\":true" +
			"}," +
			"{" +
			"\"c1\":2.0123," +
			"\"c2\\nsecond line\":\"2016-02-01 16:00:00.123456\"," +
			"\"c3\":\"abcdef\"" +
			"}," +
			"{" +
			"\"c1\":34567890," +
			"\"c2\\nsecond line\":\" abc\\\\defghi\\/jklmn\\topqrstuvwxyzabcdefg\\nhi\\\"jk\\n\"," +
			"\"c3\":null" +
			"}" +
			"]",
	},
}

func TestEncodeView(t *testing.T) {
	flags := cmd.GetFlags()

	for _, v := range encodeViewTests {
		flags.Format = v.Format

		flags.LineBreak = cmd.LF
		if v.LineBreak != "" && v.LineBreak != cmd.LF {
			flags.LineBreak = v.LineBreak
		}
		flags.WithoutHeader = false
		if v.WithoutHeader {
			flags.WithoutHeader = true
		}
		flags.WriteDelimiter = ','
		if v.WriteDelimiter != 0 {
			flags.WriteDelimiter = v.WriteDelimiter
		}

		s, err := EncodeView(v.View)
		if err != nil {
			if len(v.Error) < 1 {
				t.Errorf("%s: unexpected error %q", v.Name, err)
			} else if err.Error() != v.Error {
				t.Errorf("%s: error %q, want error %q", v.Name, err.Error(), v.Error)
			}
			continue
		}
		if 0 < len(v.Error) {
			t.Errorf("%s: no error, want error %q", v.Name, v.Error)
			continue
		}
		if s != v.Result {
			t.Errorf("%s, result = %q, want %q", v.Name, s, v.Result)
		}
	}
}
