package cmd

// AnnotAddInput is the input to annotate add.
type AnnotAddInput struct {
	Path    string
	Content string
}

// AnnotAdd appends an annotation.
func (e *Engine) AnnotAdd(in AnnotAddInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	a, err := e.Store.AnnotationAdd(e.Actor, fi.ID, in.Content)
	if err != nil {
		return nil, err
	}
	return &Result{FileID: &fi.ID, Annot: &AnnotResult{Annotation: *a}}, nil
}

// AnnotListInput is the input to annotate list.
type AnnotListInput struct {
	Path           string
	IncludeRemoved bool
}

// AnnotList returns active (or all) annotations.
func (e *Engine) AnnotList(in AnnotListInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	anns, err := e.Store.AnnotationList(fi.ID, in.IncludeRemoved)
	if err != nil {
		return nil, err
	}
	return &Result{FileID: &fi.ID, Annots: &AnnotsResult{Annotations: anns}}, nil
}

// AnnotGetInput is the input to annotate get.
type AnnotGetInput struct {
	Path string
	ID   int64
}

// AnnotGet returns one annotation.
func (e *Engine) AnnotGet(in AnnotGetInput) (*Result, error) {
	a, err := e.Store.AnnotationGet(in.ID)
	if err != nil {
		return nil, err
	}
	return &Result{FileID: &a.FileID, Annot: &AnnotResult{Annotation: *a}}, nil
}

// AnnotRemoveInput is the input to annotate remove.
type AnnotRemoveInput struct {
	Path string
	ID   int64
}

// AnnotRemove soft-deletes an annotation.
func (e *Engine) AnnotRemove(in AnnotRemoveInput) (*Result, error) {
	if err := e.Store.AnnotationRemove(in.ID); err != nil {
		return nil, err
	}
	return &Result{}, nil
}

// AnnotSearchInput is the input to annotate search.
type AnnotSearchInput struct{ Query string }

// AnnotSearch searches across all files.
func (e *Engine) AnnotSearch(in AnnotSearchInput) (*Result, error) {
	anns, paths, err := e.Store.AnnotationSearch(in.Query)
	if err != nil {
		return nil, err
	}
	return &Result{AnnotsSearch: &AnnotsSearchResult{Annotations: anns, Paths: paths}}, nil
}
