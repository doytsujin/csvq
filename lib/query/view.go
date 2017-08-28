package query

import (
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mithrandie/csvq/lib/cmd"
	"github.com/mithrandie/csvq/lib/csv"
	"github.com/mithrandie/csvq/lib/parser"
	"github.com/mithrandie/csvq/lib/ternary"
)

type View struct {
	Header   Header
	Records  Records
	FileInfo *FileInfo

	Filter *Filter

	selectFields []int
	selectLabels []string
	isGrouped    bool

	filteredIndices []int

	comparisonKeys []string

	sortValues        [][]*SortValue
	recordSortValues  []SortValues
	sortDirections    []int
	sortNullPositions []int

	offset int

	OperatedRecords int
	OperatedFields  int

	UseInternalId bool
}

func NewView() *View {
	return &View{
		UseInternalId: false,
	}
}

func (view *View) Load(clause parser.FromClause, filter *Filter) error {
	if clause.Tables == nil {
		var obj parser.Expression
		if IsReadableFromStdin() {
			obj = parser.Stdin{Stdin: "stdin"}
		} else {
			obj = parser.Dual{}
		}
		clause.Tables = []parser.Expression{parser.Table{Object: obj}}
	}

	views := make([]*View, len(clause.Tables))
	for i, v := range clause.Tables {
		loaded, err := loadView(v, filter, view.UseInternalId)
		if err != nil {
			return err
		}
		views[i] = loaded
	}

	view.Header = views[0].Header
	view.Records = views[0].Records
	view.FileInfo = views[0].FileInfo

	for i := 1; i < len(views); i++ {
		CrossJoin(view, views[i])
	}

	view.Filter = filter
	return nil
}

func (view *View) LoadFromTableIdentifier(table parser.Expression, filter *Filter) error {
	fromClause := parser.FromClause{
		Tables: []parser.Expression{
			parser.Table{Object: table},
		},
	}

	return view.Load(fromClause, filter)
}

func loadView(tableExpr parser.Expression, filter *Filter, useInternalId bool) (*View, error) {
	if parentheses, ok := tableExpr.(parser.Parentheses); ok {
		return loadView(parentheses.Expr, filter, useInternalId)
	}

	table := tableExpr.(parser.Table)

	var view *View
	var err error

	switch table.Object.(type) {
	case parser.Dual:
		view = loadDualView()
	case parser.Stdin:
		delimiter := cmd.GetFlags().Delimiter
		if delimiter == cmd.UNDEF {
			delimiter = ','
		}
		fileInfo := &FileInfo{
			Path:      table.Object.String(),
			Delimiter: delimiter,
			Temporary: true,
		}

		if !filter.TempViewsList[len(filter.TempViewsList)-1].Exists(fileInfo.Path) {
			if !IsReadableFromStdin() {
				return nil, NewStdinEmptyError(table.Object.(parser.Stdin))
			}

			file := os.Stdin
			defer file.Close()

			loadView, err := loadViewFromFile(file, fileInfo)
			if err != nil {
				return nil, err
			}
			loadView.FileInfo.InitialRecords = loadView.Records.Copy()
			filter.TempViewsList[len(filter.TempViewsList)-1].Set(loadView)
		}
		if err = filter.AliasesList.Add(table.Name(), fileInfo.Path); err != nil {
			return nil, err
		}

		pathIdent := parser.Identifier{Literal: table.Object.String()}
		if useInternalId {
			view, _ = filter.TempViewsList[len(filter.TempViewsList)-1].GetWithInternalId(pathIdent)
		} else {
			view, _ = filter.TempViewsList[len(filter.TempViewsList)-1].Get(pathIdent)
		}
		if !strings.EqualFold(table.Object.String(), table.Name().Literal) {
			view.Header.Update(table.Name().Literal, nil)
		}
	case parser.Identifier:
		tableIdentifier := table.Object.(parser.Identifier)
		if filter.RecursiveTable != nil && strings.EqualFold(tableIdentifier.Literal, filter.RecursiveTable.Name.Literal) && filter.RecursiveTmpView != nil {
			view = filter.RecursiveTmpView
			if !strings.EqualFold(filter.RecursiveTable.Name.Literal, table.Name().Literal) {
				view.Header.Update(table.Name().Literal, nil)
			}
		} else if ct, err := filter.InlineTablesList.Get(tableIdentifier); err == nil {
			if err = filter.AliasesList.Add(table.Name(), ""); err != nil {
				return nil, err
			}
			view = ct
			if !strings.EqualFold(tableIdentifier.Literal, table.Name().Literal) {
				view.Header.Update(table.Name().Literal, nil)
			}
		} else {
			var fileInfo *FileInfo
			var commonTableName string

			if filter.TempViewsList.Exists(tableIdentifier.Literal) {
				fileInfo = &FileInfo{
					Path: tableIdentifier.Literal,
				}

				commonTableName = parser.FormatTableName(fileInfo.Path)

				pathIdent := parser.Identifier{Literal: fileInfo.Path}
				if useInternalId {
					view, _ = filter.TempViewsList.GetWithInternalId(pathIdent)
				} else {
					view, _ = filter.TempViewsList.Get(pathIdent)
				}
			} else {
				flags := cmd.GetFlags()

				fileInfo, err = NewFileInfoForCreate(tableIdentifier, flags.Repository, flags.Delimiter)
				if err != nil {
					return nil, err
				}

				if !ViewCache.Exists(fileInfo.Path) {
					fileInfo, err = NewFileInfo(tableIdentifier, flags.Repository, flags.Delimiter)
					if err != nil {
						return nil, err
					}

					if !ViewCache.Exists(fileInfo.Path) {
						file, err := os.Open(fileInfo.Path)
						if err != nil {
							return nil, NewReadFileError(tableIdentifier, err.Error())
						}
						defer file.Close()
						loadView, err := loadViewFromFile(file, fileInfo)
						if err != nil {
							return nil, NewCsvParsingError(tableIdentifier, fileInfo.Path, err.Error())
						}
						ViewCache.Set(loadView)
					}
				}
				commonTableName = parser.FormatTableName(fileInfo.Path)

				pathIdent := parser.Identifier{Literal: fileInfo.Path}
				if useInternalId {
					view, _ = ViewCache.GetWithInternalId(pathIdent)
				} else {
					view, _ = ViewCache.Get(pathIdent)
				}
			}

			if err = filter.AliasesList.Add(table.Name(), fileInfo.Path); err != nil {
				return nil, err
			}

			if !strings.EqualFold(commonTableName, table.Name().Literal) {
				view.Header.Update(table.Name().Literal, nil)
			}
		}
	case parser.Join:
		join := table.Object.(parser.Join)
		view, err = loadView(join.Table, filter, useInternalId)
		if err != nil {
			return nil, err
		}
		view2, err := loadView(join.JoinTable, filter, useInternalId)
		if err != nil {
			return nil, err
		}

		condition, includeFields, excludeFields, err := ParseJoinCondition(join, view, view2)
		if err != nil {
			return nil, err
		}

		joinType := join.JoinType.Token
		if join.JoinType.IsEmpty() {
			if join.Direction.IsEmpty() {
				joinType = parser.INNER
			} else {
				joinType = parser.OUTER
			}
		}

		switch joinType {
		case parser.CROSS:
			CrossJoin(view, view2)
		case parser.INNER:
			if err = InnerJoin(view, view2, condition, filter); err != nil {
				return nil, err
			}
		case parser.OUTER:
			if err = OuterJoin(view, view2, condition, join.Direction.Token, filter); err != nil {
				return nil, err
			}
		}

		includeIndices := make([]int, 0, len(includeFields))
		excludeIndices := make([]int, 0, len(includeFields))
		if includeFields != nil {
			for i := range includeFields {
				idx, _ := view.Header.Contains(includeFields[i])
				includeIndices = append(includeIndices, idx)

				idx, _ = view.Header.Contains(excludeFields[i])
				excludeIndices = append(excludeIndices, idx)
			}

			fieldIndices := make([]int, 0, view.FieldLen())
			header := make(Header, 0, view.FieldLen()-len(excludeIndices))
			for _, idx := range includeIndices {
				view.Header[idx].View = ""
				view.Header[idx].Number = 0
				view.Header[idx].IsJoinColumn = true
				header = append(header, view.Header[idx])
				fieldIndices = append(fieldIndices, idx)
			}
			for i := range view.Header {
				if InIntSlice(i, excludeIndices) || InIntSlice(i, includeIndices) {
					continue
				}
				header = append(header, view.Header[i])
				fieldIndices = append(fieldIndices, i)
			}
			view.Header = header

			cpu := NumberOfCPU(view.RecordLen())
			wg := sync.WaitGroup{}
			for i := 0; i < cpu; i++ {
				wg.Add(1)
				go func(thIdx int) {
					start, end := RecordRange(thIdx, view.RecordLen(), cpu)

					for i := start; i < end; i++ {
						record := make(Record, len(fieldIndices))
						for j, idx := range fieldIndices {
							record[j] = view.Records[i][idx]
						}
						view.Records[i] = record
					}

					wg.Done()
				}(i)
			}
			wg.Wait()
		}

	case parser.Subquery:
		subquery := table.Object.(parser.Subquery)
		view, err = Select(subquery.Query, filter)
		if table.Alias != nil {
			if err = filter.AliasesList.Add(table.Alias.(parser.Identifier), ""); err != nil {
				return nil, err
			}
		}
		if err == nil {
			view.Header.Update(table.Name().Literal, nil)
		}
	}

	return view, err
}

func loadViewFromFile(file *os.File, fileInfo *FileInfo) (*View, error) {
	flags := cmd.GetFlags()

	r := cmd.GetReader(file, flags.Encoding)

	reader := csv.NewReader(r)
	reader.Delimiter = fileInfo.Delimiter
	reader.WithoutNull = flags.WithoutNull

	var err error
	var header []string
	if !flags.NoHeader {
		header, err = reader.ReadHeader()
		if err != nil && err != csv.EOF {
			return nil, err
		}
	}

	records := Records{}
	rowch := make(chan []csv.Field, 1000)
	fieldch := make(chan []parser.Primary, 1000)

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		for {
			primaries, ok := <-fieldch
			if !ok {
				break
			}
			records = append(records, NewRecord(primaries))
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		for {
			row, ok := <-rowch
			if !ok {
				break
			}
			fields := make([]parser.Primary, len(row))
			for i, v := range row {
				fields[i] = v.ToPrimary()
			}
			fieldch <- fields
		}
		close(fieldch)
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		for {
			record, e := reader.Read()
			if e == csv.EOF {
				break
			}
			if e != nil {
				err = e
				break
			}
			rowch <- record
		}
		close(rowch)
		wg.Done()
	}()

	wg.Wait()

	if err != nil {
		return nil, err
	}

	if header == nil {
		header = make([]string, reader.FieldsPerRecord)
		for i := 0; i < reader.FieldsPerRecord; i++ {
			header[i] = "c" + strconv.Itoa(i+1)
		}
	}

	fileInfo.NoHeader = flags.NoHeader
	fileInfo.Encoding = flags.Encoding
	fileInfo.LineBreak = reader.LineBreak
	if fileInfo.LineBreak == "" {
		fileInfo.LineBreak = flags.LineBreak
	}

	view := NewView()
	view.Header = NewHeader(parser.FormatTableName(fileInfo.Path), header)
	view.Records = records
	view.FileInfo = fileInfo
	return view, nil
}

func loadDualView() *View {
	view := View{
		Header:  NewDualHeader(),
		Records: make([]Record, 1),
	}
	view.Records[0] = NewEmptyRecord(1)
	return &view
}

func NewViewFromGroupedRecord(filterRecord FilterRecord) *View {
	view := new(View)
	view.Header = filterRecord.View.Header
	record := filterRecord.View.Records[filterRecord.RecordIndex]

	view.Records = make([]Record, record.GroupLen())
	for i := 0; i < record.GroupLen(); i++ {
		view.Records[i] = make(Record, view.FieldLen())
		for j, cell := range record {
			grpIdx := i
			if cell.Len() < 2 {
				grpIdx = 0
			}
			view.Records[i][j] = NewCell(cell.GroupedPrimary(grpIdx))
		}
	}

	view.Filter = filterRecord.View.Filter

	return view
}

func (view *View) Where(clause parser.WhereClause) error {
	indices, err := view.filter(clause.Filter)
	if err != nil {
		return err
	}

	view.filteredIndices = indices
	return nil
}

func (view *View) filter(condition parser.Expression) ([]int, error) {
	cpu := NumberOfCPU(view.RecordLen())

	var err error
	results := make([]bool, view.RecordLen())

	wg := sync.WaitGroup{}
	for i := 0; i < cpu; i++ {
		wg.Add(1)
		go func(thIdx int) {
			start, end := RecordRange(thIdx, view.RecordLen(), cpu)
			filter := NewFilterForSequentialEvaluation(view, view.Filter)

		FilterLoop:
			for i := start; i < end; i++ {
				if err != nil {
					break FilterLoop
				}

				filter.Records[0].RecordIndex = i
				primary, e := filter.Evaluate(condition)
				if e != nil {
					err = e
					break FilterLoop
				}
				if primary.Ternary() == ternary.TRUE {
					results[i] = true
				}
			}

			wg.Done()
		}(i)
	}
	wg.Wait()

	if err != nil {
		return nil, err
	}

	indices := make([]int, 0, len(results))
	for i, ok := range results {
		if ok {
			indices = append(indices, i)
		}
	}

	return indices, nil
}

func (view *View) Extract() {
	records := make(Records, len(view.filteredIndices))
	for i, idx := range view.filteredIndices {
		records[i] = view.Records[idx]
	}
	view.Records = records
	view.filteredIndices = nil
}

func (view *View) GroupBy(clause parser.GroupByClause) error {
	return view.group(clause.Items)
}

func (view *View) group(items []parser.Expression) error {
	cpu := NumberOfCPU(view.RecordLen())

	var err error
	keys := make([]string, view.RecordLen())

	wg := sync.WaitGroup{}
	for i := 0; i < cpu; i++ {
		wg.Add(1)
		go func(thIdx int) {
			start, end := RecordRange(thIdx, view.RecordLen(), cpu)

			filter := NewFilterForSequentialEvaluation(view, view.Filter)
			values := make([]parser.Primary, len(items))

		GroupLoop:
			for i := start; i < end; i++ {
				if err != nil {
					break GroupLoop
				}

				filter.Records[0].RecordIndex = i
				for j, item := range items {
					p, e := filter.Evaluate(item)
					if e != nil {
						err = e
						break GroupLoop
					}
					values[j] = p
				}
				keys[i] = SerializeComparisonKeys(values)
			}

			wg.Done()
		}(i)
	}
	wg.Wait()

	if err != nil {
		return err
	}

	groups := make(map[string][]int)
	groupKeys := []string{}
	for i, key := range keys {
		if _, ok := groups[key]; ok {
			groups[key] = append(groups[key], i)
		} else {
			groups[key] = []int{i}
			groupKeys = append(groupKeys, key)
		}
	}

	records := make(Records, len(groupKeys))
	for i, groupKey := range groupKeys {
		record := make(Record, view.FieldLen())
		indices := groups[groupKey]

		for j := 0; j < view.FieldLen(); j++ {
			primaries := make([]parser.Primary, len(indices))
			for k, idx := range indices {
				primaries[k] = view.Records[idx][j].Primary()
			}
			record[j] = NewGroupCell(primaries)
		}

		records[i] = record
	}

	view.Records = records
	view.isGrouped = true
	for _, item := range items {
		switch item.(type) {
		case parser.FieldReference, parser.ColumnNumber:
			idx, _ := view.FieldIndex(item)
			view.Header[idx].IsGroupKey = true
		}
	}
	return nil
}

func (view *View) Having(clause parser.HavingClause) error {
	indices, err := view.filter(clause.Filter)
	if err != nil {
		if _, ok := err.(*NotGroupingRecordsError); ok {
			view.group(nil)
			indices, err = view.filter(clause.Filter)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	view.filteredIndices = indices
	return nil
}

func (view *View) Select(clause parser.SelectClause) error {
	var parseAllColumns = func(view *View, fields []parser.Expression) []parser.Expression {
		insertIdx := -1

		for i, field := range fields {
			if _, ok := field.(parser.Field).Object.(parser.AllColumns); ok {
				insertIdx = i
				break
			}
		}

		if insertIdx < 0 {
			return fields
		}

		columns := view.Header.TableColumns()
		insertLen := len(columns)
		insert := make([]parser.Expression, insertLen)
		for i, c := range columns {
			insert[i] = parser.Field{
				Object: c,
			}
		}

		list := make([]parser.Expression, len(fields)-1+insertLen)
		for i, field := range fields {
			switch {
			case i == insertIdx:
				continue
			case i < insertIdx:
				list[i] = field
			default:
				list[i+insertLen-1] = field
			}
		}
		for i, field := range insert {
			list[i+insertIdx] = field
		}

		return list
	}

	var evalFields = func(view *View, fields []parser.Expression) error {
		fieldsObjects := make([]parser.Expression, len(fields))
		for i, f := range fields {
			fieldsObjects[i] = f.(parser.Field).Object
		}
		if err := view.ExtendRecordCapacity(fieldsObjects); err != nil {
			return err
		}

		view.selectFields = make([]int, len(fields))
		view.selectLabels = make([]string, len(fields))
		for i, f := range fields {
			field := f.(parser.Field)
			alias := ""
			if field.Alias != nil {
				alias = field.Alias.(parser.Identifier).Literal
			}
			idx, err := view.evalColumn(field.Object, alias)
			if err != nil {
				return err
			}
			view.selectFields[i] = idx
			view.selectLabels[i] = field.Name()
		}
		return nil
	}

	fields := parseAllColumns(view, clause.Fields)

	origFieldLen := view.FieldLen()
	err := evalFields(view, fields)
	if err != nil {
		if _, ok := err.(*NotGroupingRecordsError); ok {
			view.Header = view.Header[:origFieldLen]
			if 0 < view.RecordLen() && view.FieldLen() < len(view.Records[0]) {
				for i := range view.Records {
					view.Records[i] = view.Records[i][:origFieldLen]
				}
			}

			view.group(nil)
			err = evalFields(view, fields)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	if clause.IsDistinct() {
		view.GenerateComparisonKeys()
		records := make(Records, 0, view.RecordLen())
		values := make(map[string]bool)
		for i, v := range view.Records {
			if !values[view.comparisonKeys[i]] {
				values[view.comparisonKeys[i]] = true

				record := make(Record, len(view.selectFields))
				for j, idx := range view.selectFields {
					record[j] = v[idx]
				}
				records = append(records, record)
			}
		}

		hfields := NewEmptyHeader(len(view.selectFields))
		for i, idx := range view.selectFields {
			hfields[i] = view.Header[idx]
			view.selectFields[i] = i
		}

		view.Header = hfields
		view.Records = records
		view.comparisonKeys = nil
		view.sortValues = nil
	}

	return nil
}

func (view *View) GenerateComparisonKeys() {
	view.comparisonKeys = make([]string, view.RecordLen())

	cpu := NumberOfCPU(view.RecordLen())
	wg := sync.WaitGroup{}
	for i := 0; i < cpu; i++ {
		wg.Add(1)
		go func(thIdx int) {
			start, end := RecordRange(thIdx, view.RecordLen(), cpu)

			var primaries []parser.Primary
			if view.selectFields != nil {
				primaries = make([]parser.Primary, len(view.selectFields))
			}

			for i := start; i < end; i++ {
				if view.selectFields != nil {
					for j, idx := range view.selectFields {
						primaries[j] = view.Records[i][idx].Primary()
					}
					view.comparisonKeys[i] = SerializeComparisonKeys(primaries)
				} else {
					view.comparisonKeys[i] = view.Records[i].SerializeComparisonKeys()
				}
			}

			wg.Done()
		}(i)
	}
	wg.Wait()
}

func (view *View) SelectAllColumns() error {
	selectClause := parser.SelectClause{
		Fields: []parser.Expression{
			parser.Field{Object: parser.AllColumns{}},
		},
	}
	return view.Select(selectClause)
}

func (view *View) OrderBy(clause parser.OrderByClause) error {
	orderValues := make([]parser.Expression, len(clause.Items))
	for i, item := range clause.Items {
		orderValues[i] = item.(parser.OrderItem).Value
	}
	if err := view.ExtendRecordCapacity(orderValues); err != nil {
		return err
	}

	sortIndices := make([]int, len(clause.Items))
	for i, v := range clause.Items {
		oi := v.(parser.OrderItem)
		idx, err := view.evalColumn(oi.Value, "")
		if err != nil {
			return err
		}
		sortIndices[i] = idx
	}

	view.recordSortValues = make([]SortValues, view.RecordLen())
	view.sortDirections = make([]int, len(clause.Items))
	view.sortNullPositions = make([]int, len(clause.Items))

	for i, v := range clause.Items {
		oi := v.(parser.OrderItem)
		if oi.Direction.IsEmpty() {
			view.sortDirections[i] = parser.ASC
		} else {
			view.sortDirections[i] = oi.Direction.Token
		}

		if oi.Position.IsEmpty() {
			switch view.sortDirections[i] {
			case parser.ASC:
				view.sortNullPositions[i] = parser.FIRST
			default: //parser.DESC
				view.sortNullPositions[i] = parser.LAST
			}
		} else {
			view.sortNullPositions[i] = oi.Position.Token
		}
	}

	cpu := NumberOfCPU(view.RecordLen())

	wg := sync.WaitGroup{}
	for i := 0; i < cpu; i++ {
		wg.Add(1)
		go func(thIdx int) {
			start, end := RecordRange(thIdx, view.RecordLen(), cpu)

			for i := start; i < end; i++ {
				if view.sortValues != nil && view.sortValues[i] == nil {
					view.sortValues[i] = make([]*SortValue, cap(view.Records[i]))
				}

				sortValues := make(SortValues, len(sortIndices))
				for j, idx := range sortIndices {
					if view.sortValues != nil && idx < len(view.sortValues[i]) && view.sortValues[i][idx] != nil {
						sortValues[j] = view.sortValues[i][idx]
					} else {
						sortValues[j] = NewSortValue(view.Records[i][idx].Primary())
						if view.sortValues != nil && idx < len(view.sortValues[i]) {
							view.sortValues[i][idx] = sortValues[j]
						}
					}
				}
				view.recordSortValues[i] = sortValues
			}

			wg.Done()
		}(i)
	}
	wg.Wait()

	sort.Sort(view)
	return nil
}

func (view *View) additionalColumns(expr parser.Expression) ([]string, error) {
	list := []string{}

	switch expr.(type) {
	case parser.FieldReference, parser.ColumnNumber:
		return nil, nil
	case parser.Function:
		if udfn, err := view.Filter.FunctionsList.Get(expr, expr.(parser.Function).Name); err == nil {
			if udfn.IsAggregate && !view.isGrouped {
				return nil, NewNotGroupingRecordsError(expr, expr.(parser.Function).Name)
			}
		}
	case parser.AggregateFunction:
		if !view.isGrouped {
			return nil, NewNotGroupingRecordsError(expr, expr.(parser.AggregateFunction).Name)
		}
	case parser.ListAgg:
		if !view.isGrouped {
			return nil, NewNotGroupingRecordsError(expr, expr.(parser.ListAgg).ListAgg)
		}
	case parser.AnalyticFunction:
		fn := expr.(parser.AnalyticFunction)
		pvalues := fn.AnalyticClause.PartitionValues()
		ovalues := []parser.Expression(nil)
		if fn.AnalyticClause.OrderByClause != nil {
			ovalues = fn.AnalyticClause.OrderByClause.(parser.OrderByClause).Items
		}

		if pvalues != nil {
			for _, pvalue := range pvalues {
				columns, err := view.additionalColumns(pvalue)
				if err != nil {
					return nil, err
				}
				for _, s := range columns {
					if !InStrSliceWithCaseInsensitive(s, list) {
						list = append(list, s)
					}
				}
			}
		}
		if ovalues != nil {
			for _, value := range ovalues {
				item := value.(parser.OrderItem)
				columns, err := view.additionalColumns(item.Value)
				if err != nil {
					return nil, err
				}
				for _, s := range columns {
					if !InStrSliceWithCaseInsensitive(s, list) {
						list = append(list, s)
					}
				}
			}
		}
	}

	if _, err := view.Header.ContainsObject(expr); err != nil {
		s := expr.String()
		if !InStrSliceWithCaseInsensitive(s, list) {
			list = append(list, s)
		}
	}

	return list, nil
}

func (view *View) ExtendRecordCapacity(exprs []parser.Expression) error {
	additions := []string{}
	for _, expr := range exprs {
		columns, err := view.additionalColumns(expr)
		if err != nil {
			return err
		}
		for _, s := range columns {
			if !InStrSliceWithCaseInsensitive(s, additions) {
				additions = append(additions, s)
			}
		}
	}

	currentLen := view.FieldLen()
	fieldCap := currentLen + len(additions)

	if 0 < view.RecordLen() && fieldCap <= cap(view.Records[0]) {
		return nil
	}

	cpu := NumberOfCPU(view.RecordLen())
	wg := sync.WaitGroup{}
	for i := 0; i < cpu; i++ {
		wg.Add(1)
		go func(thIdx int) {
			start, end := RecordRange(thIdx, view.RecordLen(), cpu)
			for i := start; i < end; i++ {
				record := make(Record, currentLen, fieldCap)
				copy(record, view.Records[i])
				view.Records[i] = record
			}
			wg.Done()
		}(i)
	}
	wg.Wait()
	return nil
}

func (view *View) evalColumn(obj parser.Expression, alias string) (idx int, err error) {
	switch obj.(type) {
	case parser.FieldReference, parser.ColumnNumber:
		if idx, err = view.FieldIndex(obj); err != nil {
			return
		}
		if view.isGrouped && view.Header[idx].IsFromTable && !view.Header[idx].IsGroupKey {
			err = NewFieldNotGroupKeyError(obj)
			return
		}
	default:
		idx, err = view.Header.ContainsObject(obj)
		if err != nil {
			err = nil

			if analyticFunction, ok := obj.(parser.AnalyticFunction); ok {
				err = view.evalAnalyticFunction(analyticFunction)
				if err != nil {
					return
				}
			} else {
				cpu := NumberOfCPU(view.RecordLen())

				wg := sync.WaitGroup{}
				for i := 0; i < cpu; i++ {
					wg.Add(1)
					go func(thIdx int) {
						start, end := RecordRange(thIdx, view.RecordLen(), cpu)
						filter := NewFilterForSequentialEvaluation(view, view.Filter)

					EvalColumnLoop:
						for i := start; i < end; i++ {
							if err != nil {
								break EvalColumnLoop
							}

							var primary parser.Primary
							filter.Records[0].RecordIndex = i

							primary, err = filter.Evaluate(obj)
							if err != nil {
								break EvalColumnLoop
							}
							view.Records[i] = append(view.Records[i], NewCell(primary))
						}

						wg.Done()
					}(i)
				}
				wg.Wait()

				if err != nil {
					return
				}
			}
			view.Header, idx = AddHeaderField(view.Header, parser.FieldIdentifier(obj), alias)
		}
	}

	if 0 < len(alias) {
		if !strings.EqualFold(view.Header[idx].Column, alias) && !InStrSliceWithCaseInsensitive(alias, view.Header[idx].Aliases) {
			view.Header[idx].Aliases = append(view.Header[idx].Aliases, alias)
		}
	}

	return
}

func (view *View) evalAnalyticFunction(expr parser.AnalyticFunction) error {
	name := strings.ToUpper(expr.Name)
	if _, ok := AggregateFunctions[name]; !ok {
		if _, ok := AnalyticFunctions[name]; !ok {
			if udfn, err := view.Filter.FunctionsList.Get(expr, expr.Name); err != nil || !udfn.IsAggregate {
				return NewFunctionNotExistError(expr, expr.Name)
			}
		}
	}

	var partitionIndices []int
	if expr.AnalyticClause.Partition != nil {
		partitionExprs := expr.AnalyticClause.PartitionValues()

		partitionIndices = make([]int, len(partitionExprs))
		for i, pexpr := range partitionExprs {
			idx, err := view.evalColumn(pexpr, "")
			if err != nil {
				return err
			}
			partitionIndices[i] = idx
		}
	}

	if view.sortValues == nil {
		view.sortValues = make([][]*SortValue, view.RecordLen())
	}

	if expr.AnalyticClause.OrderByClause != nil {
		err := view.OrderBy(expr.AnalyticClause.OrderByClause.(parser.OrderByClause))
		if err != nil {
			return err
		}
	}

	err := Analyze(view, expr, partitionIndices)

	view.recordSortValues = nil
	view.sortDirections = nil
	view.sortNullPositions = nil

	return err
}

func (view *View) Offset(clause parser.OffsetClause) error {
	value, err := view.Filter.Evaluate(clause.Value)
	if err != nil {
		return err
	}
	number := parser.PrimaryToInteger(value)
	if parser.IsNull(number) {
		return NewInvalidOffsetNumberError(clause)
	}
	view.offset = int(number.(parser.Integer).Value())
	if view.offset < 0 {
		view.offset = 0
	}

	if view.RecordLen() <= view.offset {
		view.Records = Records{}
	} else {
		view.Records = view.Records[view.offset:]
		records := make(Records, len(view.Records))
		copy(records, view.Records)
		view.Records = records
	}
	return nil
}

func (view *View) Limit(clause parser.LimitClause) error {
	value, err := view.Filter.Evaluate(clause.Value)
	if err != nil {
		return err
	}

	var limit int
	if clause.IsPercentage() {
		number := parser.PrimaryToFloat(value)
		if parser.IsNull(number) {
			return NewInvalidLimitPercentageError(clause)
		}
		percentage := number.(parser.Float).Value()
		if 100 < percentage {
			limit = 100
		} else if percentage < 0 {
			limit = 0
		} else {
			limit = int(math.Ceil(float64(view.RecordLen()+view.offset) * percentage / 100))
		}
	} else {
		number := parser.PrimaryToInteger(value)
		if parser.IsNull(number) {
			return NewInvalidLimitNumberError(clause)
		}
		limit = int(number.(parser.Integer).Value())
		if limit < 0 {
			limit = 0
		}
	}

	if view.RecordLen() <= limit {
		return nil
	}

	if clause.IsWithTies() && view.recordSortValues != nil {
		bottomSortValues := view.recordSortValues[limit-1]
		for limit < view.RecordLen() {
			if !bottomSortValues.EquivalentTo(view.recordSortValues[limit]) {
				break
			}
			limit++
		}
	}

	view.Records = view.Records[:limit]
	records := make(Records, view.RecordLen())
	copy(records, view.Records)
	view.Records = records
	return nil
}

func (view *View) InsertValues(fields []parser.Expression, list []parser.Expression) error {
	valuesList := make([][]parser.Primary, len(list))

	for i, item := range list {
		rv := item.(parser.RowValue)
		values, err := view.Filter.evalRowValue(rv)
		if err != nil {
			return err
		}
		if len(fields) != len(values) {
			return NewInsertRowValueLengthError(rv, len(fields))
		}

		valuesList[i] = values
	}

	return view.insert(fields, valuesList)
}

func (view *View) InsertFromQuery(fields []parser.Expression, query parser.SelectQuery) error {
	insertView, err := Select(query, view.Filter)
	if err != nil {
		return err
	}
	if len(fields) != insertView.FieldLen() {
		return NewInsertSelectFieldLengthError(query, len(fields))
	}

	valuesList := make([][]parser.Primary, insertView.RecordLen())

	for i, record := range insertView.Records {
		values := make([]parser.Primary, insertView.FieldLen())
		for j, cell := range record {
			values[j] = cell.Primary()
		}
		valuesList[i] = values
	}

	return view.insert(fields, valuesList)
}

func (view *View) insert(fields []parser.Expression, valuesList [][]parser.Primary) error {
	var valueIndex = func(i int, list []int) int {
		for j, v := range list {
			if i == v {
				return j
			}
		}
		return -1
	}

	fieldIndices, err := view.FieldIndices(fields)
	if err != nil {
		return err
	}

	records := make([]Record, len(valuesList))
	for i, values := range valuesList {
		record := make(Record, view.FieldLen())
		for j := 0; j < view.FieldLen(); j++ {
			idx := valueIndex(j, fieldIndices)
			if idx < 0 {
				record[j] = NewCell(parser.NewNull())
			} else {
				record[j] = NewCell(values[idx])
			}
		}
		records[i] = record
	}

	view.Records = append(view.Records, records...)
	view.OperatedRecords = len(valuesList)
	return nil
}

func (view *View) Fix() {
	resize := false
	if len(view.selectFields) < view.FieldLen() {
		resize = true
	} else {
		for i := 0; i < view.FieldLen(); i++ {
			if view.selectFields[i] != i {
				resize = true
				break
			}
		}
	}

	if resize {
		cpu := NumberOfCPU(view.RecordLen())

		wg := sync.WaitGroup{}
		for i := 0; i < cpu; i++ {
			wg.Add(1)
			go func(thIdx int) {
				start, end := RecordRange(thIdx, view.RecordLen(), cpu)

				for i := start; i < end; i++ {
					record := make(Record, len(view.selectFields))
					for j, idx := range view.selectFields {
						if 1 < view.Records[i].GroupLen() {
							record[j] = NewCell(view.Records[i][idx].Primary())
						} else {
							record[j] = view.Records[i][idx]
						}
					}
					view.Records[i] = record
				}

				wg.Done()
			}(i)
		}
		wg.Wait()
	}

	hfields := NewEmptyHeader(len(view.selectFields))

	colNumber := 0
	for i, idx := range view.selectFields {
		colNumber++

		hfields[i] = view.Header[idx]
		hfields[i].Aliases = nil
		hfields[i].Number = colNumber
		hfields[i].IsFromTable = true
		hfields[i].IsJoinColumn = false
		hfields[i].IsGroupKey = false

		if 0 < len(view.selectLabels) {
			hfields[i].Column = view.selectLabels[i]
		}
	}

	view.Header = hfields
	view.Filter = nil
	view.selectFields = nil
	view.selectLabels = nil
	view.isGrouped = false
	view.comparisonKeys = nil
	view.sortValues = nil
	view.recordSortValues = nil
	view.sortDirections = nil
	view.sortNullPositions = nil
	view.offset = 0
}

func (view *View) Union(calcView *View, all bool) {
	view.Records = append(view.Records, calcView.Records...)
	view.FileInfo = nil

	if !all {
		view.GenerateComparisonKeys()

		records := make(Records, 0, view.RecordLen())
		values := make(map[string]bool)

		for i, key := range view.comparisonKeys {
			if !values[key] {
				values[key] = true
				records = append(records, view.Records[i])
			}
		}

		view.Records = records
		view.comparisonKeys = nil
	}
}

func (view *View) Except(calcView *View, all bool) {
	view.GenerateComparisonKeys()
	calcView.GenerateComparisonKeys()

	keys := make(map[string]bool)
	for _, key := range calcView.comparisonKeys {
		if !keys[key] {
			keys[key] = true
		}
	}

	distinctKeys := make(map[string]bool)
	records := make(Records, 0, view.RecordLen())
	for i, key := range view.comparisonKeys {
		if !keys[key] {
			if !all {
				if distinctKeys[key] {
					continue
				}
				distinctKeys[key] = true
			}
			records = append(records, view.Records[i])
		}
	}
	view.Records = records
	view.FileInfo = nil
	view.comparisonKeys = nil
}

func (view *View) Intersect(calcView *View, all bool) {
	view.GenerateComparisonKeys()
	calcView.GenerateComparisonKeys()

	keys := make(map[string]bool)
	for _, key := range calcView.comparisonKeys {
		if !keys[key] {
			keys[key] = true
		}
	}

	distinctKeys := make(map[string]bool)
	records := make(Records, 0, view.RecordLen())
	for i, key := range view.comparisonKeys {
		if _, ok := keys[key]; ok {
			if !all {
				if distinctKeys[key] {
					continue
				}
				distinctKeys[key] = true
			}
			records = append(records, view.Records[i])
		}
	}
	view.Records = records
	view.FileInfo = nil
	view.comparisonKeys = nil
}

func (view *View) ListValuesForAggregateFunctions(expr parser.Expression, arg parser.Expression, distinct bool, filter *Filter) ([]parser.Primary, error) {
	cpu := NumberOfCPU(view.RecordLen())
	list := make([]parser.Primary, view.RecordLen())
	var err error

	wg := sync.WaitGroup{}
	for i := 0; i < cpu; i++ {
		wg.Add(1)
		go func(thIdx int) {
			start, end := RecordRange(thIdx, view.RecordLen(), cpu)
			filter := NewFilterForSequentialEvaluation(view, filter)

		ListAggregateFunctionLoop:
			for i := start; i < end; i++ {
				if err != nil {
					break ListAggregateFunctionLoop
				}

				filter.Records[0].RecordIndex = i
				p, e := filter.Evaluate(arg)
				if e != nil {
					if _, ok := e.(*NotGroupingRecordsError); ok {
						err = NewNestedAggregateFunctionsError(expr)
					} else {
						err = e
					}
					break ListAggregateFunctionLoop
				}
				list[i] = p
			}

			wg.Done()
		}(i)
	}
	wg.Wait()

	if err != nil {
		return nil, err
	}

	if distinct {
		list = Distinguish(list)
	}

	return list, nil
}

func (view *View) ListValuesForAnalyticFunctions(fn parser.AnalyticFunction, partition Partition) ([]parser.Primary, error) {
	cpu := NumberOfCPU(len(partition))
	list := make([]parser.Primary, len(partition))
	var err error

	wg := sync.WaitGroup{}
	for i := 0; i < cpu; i++ {
		wg.Add(1)
		go func(thIdx int) {
			start, end := RecordRange(thIdx, len(partition), cpu)
			filter := NewFilterForSequentialEvaluation(view, view.Filter)

		ListAnalyticFunctionLoop:
			for i := start; i < end; i++ {
				if err != nil {
					break ListAnalyticFunctionLoop
				}
				filter.Records[0].RecordIndex = partition[i]
				value, e := filter.Evaluate(fn.Args[0])
				if e != nil {
					err = e
					break ListAnalyticFunctionLoop
				}
				list[i] = value
			}
			wg.Done()
		}(i)
	}
	wg.Wait()

	if err != nil {
		return nil, err
	}

	if fn.IsDistinct() {
		list = Distinguish(list)
	}

	return list, nil
}

func (view *View) RestoreHeaderReferences() {
	view.Header.Update(parser.FormatTableName(view.FileInfo.Path), nil)
}

func (view *View) FieldIndex(fieldRef parser.Expression) (int, error) {
	if number, ok := fieldRef.(parser.ColumnNumber); ok {
		return view.Header.ContainsNumber(number)
	}
	return view.Header.Contains(fieldRef.(parser.FieldReference))
}

func (view *View) FieldIndices(fields []parser.Expression) ([]int, error) {
	indices := make([]int, len(fields))
	for i, v := range fields {
		idx, err := view.FieldIndex(v)
		if err != nil {
			return nil, err
		}
		indices[i] = idx
	}
	return indices, nil
}

func (view *View) FieldViewName(fieldRef parser.Expression) (string, error) {
	idx, err := view.FieldIndex(fieldRef)
	if err != nil {
		return "", err
	}
	return view.Header[idx].View, nil
}

func (view *View) InternalRecordId(ref string, recordIndex int) (int, error) {
	idx, err := view.Header.ContainsInternalId(ref)
	if err != nil {
		return -1, NewInternalRecordIdNotExistError()
	}
	internalId, ok := view.Records[recordIndex][idx].Primary().(parser.Integer)
	if !ok {
		return -1, NewInternalRecordIdEmptyError()
	}
	return int(internalId.Value()), nil
}

func (view *View) FieldLen() int {
	return view.Header.Len()
}

func (view *View) RecordLen() int {
	return view.Len()
}

func (view *View) Len() int {
	return len(view.Records)
}

func (view *View) Swap(i, j int) {
	view.Records[i], view.Records[j] = view.Records[j], view.Records[i]
	view.recordSortValues[i], view.recordSortValues[j] = view.recordSortValues[j], view.recordSortValues[i]
	if view.sortValues != nil {
		view.sortValues[i], view.sortValues[j] = view.sortValues[j], view.sortValues[i]
	}
}

func (view *View) Less(i, j int) bool {
	return view.recordSortValues[i].Less(view.recordSortValues[j], view.sortDirections, view.sortNullPositions)
}

func (view *View) Rollback() {
	view.Records = view.FileInfo.InitialRecords.Copy()
}

func (view *View) Copy() *View {
	header := view.Header.Copy()
	records := view.Records.Copy()

	return &View{
		Header:   header,
		Records:  records,
		FileInfo: view.FileInfo,
	}
}
