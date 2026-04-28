package cmd

// MarkAddInput is the input to mark add.
type MarkAddInput struct {
	Path string
	Name string
	Line int
}

// MarkAdd creates a mark.
func (e *Engine) MarkAdd(in MarkAddInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	m, err := e.Store.MarkAdd(e.Actor, fi.ID, in.Name, in.Line)
	if err != nil {
		return nil, err
	}
	return &Result{FileID: &fi.ID, Mark: &MarkResult{Mark: *m}}, nil
}

// MarkListInput is the input to mark list.
type MarkListInput struct{ Path string }

// MarkList lists marks.
func (e *Engine) MarkList(in MarkListInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	ms, err := e.Store.MarkList(fi.ID)
	if err != nil {
		return nil, err
	}
	return &Result{FileID: &fi.ID, Marks: &MarksResult{Marks: ms}}, nil
}

// MarkGetInput is the input to mark get.
type MarkGetInput struct {
	Path string
	Name string
}

// MarkGet returns a single mark.
func (e *Engine) MarkGet(in MarkGetInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	m, err := e.Store.MarkGet(fi.ID, in.Name)
	if err != nil {
		return nil, err
	}
	return &Result{FileID: &fi.ID, Mark: &MarkResult{Mark: *m}}, nil
}

// MarkRemoveInput is the input to mark remove.
type MarkRemoveInput struct {
	Path string
	Name string
}

// MarkRemove deletes a mark.
func (e *Engine) MarkRemove(in MarkRemoveInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	if err := e.Store.MarkRemove(fi.ID, in.Name); err != nil {
		return nil, err
	}
	return &Result{FileID: &fi.ID}, nil
}
